// Code generated by mockery v2.9.4. DO NOT EDIT.

package mocks

import (
	types "github.com/golangid/candi/codebase/factory/types"
	mock "github.com/stretchr/testify/mock"
)

// WorkerHandler is an autogenerated mock type for the WorkerHandler type
type WorkerHandler struct {
	mock.Mock
}

// MountHandlers provides a mock function with given fields: group
func (_m *WorkerHandler) MountHandlers(group *types.WorkerHandlerGroup) {
	_m.Called(group)
}