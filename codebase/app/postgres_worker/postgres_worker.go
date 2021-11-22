package postgresworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/golangid/candi/candihelper"
	"github.com/golangid/candi/codebase/factory"
	"github.com/golangid/candi/codebase/factory/types"
	"github.com/golangid/candi/logger"
	"github.com/golangid/candi/tracer"
	"github.com/lib/pq"
)

/*
Postgres Event Listener Worker
Listen event from data change from selected table in postgres
*/

var (
	shutdown, semaphore, startWorkerCh, releaseWorkerCh chan struct{}
)

type (
	postgresWorker struct {
		ctx           context.Context
		ctxCancelFunc func()
		opt           option

		service  factory.ServiceFactory
		listener *pq.Listener
		handlers map[string]types.WorkerHandler
		wg       sync.WaitGroup
	}
)

// NewWorker create new postgres event listener
func NewWorker(service factory.ServiceFactory, opts ...OptionFunc) factory.AppServerFactory {
	worker := &postgresWorker{
		service: service,
		opt:     getDefaultOption(),
	}

	for _, opt := range opts {
		opt(&worker.opt)
	}

	shutdown, semaphore = make(chan struct{}, 1), make(chan struct{}, worker.opt.maxGoroutines)
	startWorkerCh, releaseWorkerCh = make(chan struct{}), make(chan struct{})

	worker.handlers = make(map[string]types.WorkerHandler)
	db, listener := getListener(worker.opt.postgresDSN)
	execCreateFunctionEventQuery(db)

	for _, m := range service.GetModules() {
		if h := m.WorkerHandler(types.PostgresListener); h != nil {
			var handlerGroup types.WorkerHandlerGroup
			h.MountHandlers(&handlerGroup)
			for _, handler := range handlerGroup.Handlers {
				logger.LogYellow(fmt.Sprintf(`[POSTGRES-LISTENER] (table): %-15s  --> (module): "%s"`, `"`+handler.Pattern+`"`, m.Name()))
				worker.handlers[handler.Pattern] = handler
				execTriggerQuery(db, handler.Pattern)
			}
		}
	}

	if len(worker.handlers) == 0 {
		log.Println("postgres listener: no table event provided")
	} else {
		fmt.Printf("\x1b[34;1m⇨ Postgres Event Listener running with %d table. DSN: %s\x1b[0m\n\n",
			len(worker.handlers), candihelper.MaskingPasswordURL(worker.opt.postgresDSN))
	}

	worker.listener = listener
	worker.ctx, worker.ctxCancelFunc = context.WithCancel(context.Background())
	return worker
}

func (p *postgresWorker) Serve() {
	p.createConsulSession()

START:
	<-startWorkerCh
	p.listener.Listen(eventsConst)
	totalRunJobs := 0

	for {
		select {
		case e := <-p.listener.Notify:

			semaphore <- struct{}{}
			p.wg.Add(1)
			go func(event *pq.Notification) {
				defer func() { p.wg.Done(); <-semaphore }()

				if p.ctx.Err() != nil {
					logger.LogRed("postgres_listener > ctx root err: " + p.ctx.Err().Error())
					return
				}

				ctx := p.ctx
				message := []byte(event.Extra)

				var eventPayload EventPayload
				json.Unmarshal(message, &eventPayload)

				isLocked, releaseLock := p.opt.locker.IsLocked(
					fmt.Sprintf("%s:postgres-worker-lock:%s-%s", p.service.Name(), eventPayload.Table, eventPayload.Action),
				)
				if isLocked {
					return
				}
				defer releaseLock()

				handler := p.handlers[eventPayload.Table]
				if handler.DisableTrace {
					ctx = tracer.SkipTraceContext(ctx)
				}
				trace, ctx := tracer.StartTraceWithContext(ctx, "PostgresEventListener")
				defer func() {
					if r := recover(); r != nil {
						tracer.SetError(ctx, fmt.Errorf("panic: %v", r))
					}
					logger.LogGreen("postgres_listener > trace_url: " + tracer.GetTraceURL(ctx))
					trace.Finish()
				}()

				trace.SetTag("database", candihelper.MaskingPasswordURL(p.opt.postgresDSN))
				trace.SetTag("table_name", eventPayload.Table)
				trace.SetTag("action", eventPayload.Action)
				trace.Log("payload", event.Extra)
				if err := handler.HandlerFunc(ctx, message); err != nil {
					if handler.ErrorHandler != nil {
						handler.ErrorHandler(ctx, types.Kafka, eventPayload.Table, message, err)
					}
					trace.SetError(err)
				}
			}(e)

			// rebalance worker if run in multiple instance and using consul
			if p.opt.consul != nil {
				totalRunJobs++
				// if already running n jobs, release lock so that run in another instance
				if totalRunJobs == p.opt.consul.MaxJobRebalance {
					p.listener.Unlisten(eventsConst)
					// recreate session
					p.createConsulSession()
					<-releaseWorkerCh
					goto START
				}
			}

		case <-time.After(2 * time.Minute):
			p.listener.Ping()

		case <-shutdown:
			return
		}
	}
}

func (p *postgresWorker) Shutdown(ctx context.Context) {
	defer func() {
		if p.opt.consul != nil {
			if err := p.opt.consul.DestroySession(); err != nil {
				panic(err)
			}
		}
		log.Println("\x1b[33;1mStopping Postgres Event Listener:\x1b[0m \x1b[32;1mSUCCESS\x1b[0m")
	}()

	if len(p.handlers) == 0 {
		return
	}

	shutdown <- struct{}{}
	runningJob := len(semaphore)
	if runningJob != 0 {
		fmt.Printf("\x1b[34;1mPostgres Event Listener:\x1b[0m waiting %d job until done...\n", runningJob)
	}

	p.listener.Unlisten(eventsConst)
	p.listener.Close()
	p.wg.Wait()
	p.ctxCancelFunc()
}

func (p *postgresWorker) Name() string {
	return string(types.PostgresListener)
}

func (p *postgresWorker) createConsulSession() {
	if p.opt.consul == nil {
		go func() { startWorkerCh <- struct{}{} }()
		return
	}
	p.opt.consul.DestroySession()
	hostname, _ := os.Hostname()
	value := map[string]string{
		"hostname": hostname,
	}
	go p.opt.consul.RetryLockAcquire(value, startWorkerCh, releaseWorkerCh)
}
