package oven_controller

import "testing"

func TestNewOvenControllerDevuelveStub(t *testing.T) {
	c := NewOvenController()
	if c == nil {
		t.Fatal("NewOvenController devolvió nil")
	}
}
