package cronworker

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	oneDay   = 24 * time.Hour
	oneWeek  = 7 * oneDay
	oneMonth = 30 * oneDay
	oneYear  = 12 * oneMonth

	daily   = "daily"
	weekly  = "weekly"
	monthly = "monthly"
	yearly  = "yearly"
)

// parseAtTime with input format HH:mm:ss, will repeat every day (24 hours) in the same time
func parseAtTime(t string) (duration, nextDuration time.Duration, err error) {

	withDescriptors := strings.Split(t, "@")

	ts := strings.Split(withDescriptors[0], ":")
	if len(ts) < 2 || len(ts) > 3 {
		return 0, 0, errors.New("time format error")
	}

	var hour, min, sec int
	hour, err = strconv.Atoi(ts[0])
	if err != nil {
		return
	}

	min, err = strconv.Atoi(ts[1])
	if err != nil {
		return
	}

	if len(ts) == 3 {
		if sec, err = strconv.Atoi(ts[2]); err != nil {
			return
		}
	}

	if hour < 0 || hour > 23 || min < 0 || min > 59 || sec < 0 || sec > 59 {
		return 0, 0, errors.New("time format error")
	}

	// default value
	repeatDuration := oneDay

	if len(withDescriptors) > 1 {

		switch withDescriptors[1] {
		case daily:
			repeatDuration = oneDay
		case weekly:
			repeatDuration = oneWeek
		case monthly:
			repeatDuration = oneMonth
		case yearly:
			repeatDuration = oneYear
		default:
			repeatDuration, err = time.ParseDuration(withDescriptors[1])
			if err != nil {
				return 0, 0, fmt.Errorf(`invalid descriptor "%s" (must one of "daily", "weekly", "monthly", "yearly") or duration string`,
					withDescriptors[1])
			}
		}
	}

	now := time.Now()
	atTime := time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, now.Location())
	if now.Before(atTime) {
		duration = atTime.Sub(now)
	} else {
		duration = repeatDuration - now.Sub(atTime)
	}

	if duration < 0 {
		duration *= -1
	}

	nextDuration = repeatDuration

	return
}
