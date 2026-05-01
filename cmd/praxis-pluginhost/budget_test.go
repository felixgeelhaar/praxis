package main

import (
	"testing"
)

func TestReadBudgetEnv_DefaultsZero(t *testing.T) {
	t.Setenv(envCPUSeconds, "")
	t.Setenv(envMemBytes, "")
	b, err := readBudgetEnv()
	if err != nil {
		t.Fatalf("readBudgetEnv: %v", err)
	}
	if b.cpuSeconds != 0 || b.memBytes != 0 {
		t.Errorf("budget=%+v want zero", b)
	}
}

func TestReadBudgetEnv_ParsesValues(t *testing.T) {
	t.Setenv(envCPUSeconds, "30")
	t.Setenv(envMemBytes, "104857600")
	b, err := readBudgetEnv()
	if err != nil {
		t.Fatalf("readBudgetEnv: %v", err)
	}
	if b.cpuSeconds != 30 {
		t.Errorf("cpuSeconds=%d want 30", b.cpuSeconds)
	}
	if b.memBytes != 104857600 {
		t.Errorf("memBytes=%d want 104857600", b.memBytes)
	}
}

func TestReadBudgetEnv_RejectsBadCPU(t *testing.T) {
	t.Setenv(envCPUSeconds, "garbage")
	t.Setenv(envMemBytes, "")
	if _, err := readBudgetEnv(); err == nil {
		t.Error("expected error for bad cpu seconds")
	}
}

func TestReadBudgetEnv_RejectsBadMem(t *testing.T) {
	t.Setenv(envCPUSeconds, "")
	t.Setenv(envMemBytes, "not-a-number")
	if _, err := readBudgetEnv(); err == nil {
		t.Error("expected error for bad mem bytes")
	}
}

func TestApplyBudgetFromEnv_NoOpWhenEmpty(t *testing.T) {
	t.Setenv(envCPUSeconds, "")
	t.Setenv(envMemBytes, "")
	if err := applyBudgetFromEnv(); err != nil {
		t.Errorf("applyBudgetFromEnv with empty env: %v", err)
	}
}
