package models

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

// The tier-1 check against a real goinfer-serve, run by hand when the supervisor's use of goinfer's flags or admin
// routes changes. It skips unless both flags are given; a skip is not a pass, so read -v for --- PASS.
//
//	go test ./internal/models -run Live -v -args -goinfer-serve=/path/goinfer-serve -model-path=/path/model
//
// The memory-pressure guard is held at normal so the check measures goinfer, not the Mac's current load; use a tiny
// model (goinfer's llama-tiny fixture) for it.
var (
	liveBinary = flag.String("goinfer-serve", "", "goinfer-serve binary for the live supervisor test")
	liveModel  = flag.String("model-path", "", "model for the live supervisor test")
)

func TestLiveSupervisor(t *testing.T) {
	if *liveBinary == "" || *liveModel == "" {
		t.Skip("needs -goinfer-serve and -model-path")
	}
	dir, err := os.MkdirTemp("/tmp", "tklive")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := NewSupervisor(ChildConfig{Binary: *liveBinary, ModelPath: *liveModel, Model: "live", Ctx: 2048,
		Args: []string{"-backend", "cpu", "-quant", "int8int8"}, Dir: dir, IdleUnload: time.Minute, IdleExit: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s.pressure = func() (Pressure, error) { return PressureNormal, nil }
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srv, err := s.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := srv.Models(ctx)
	if err != nil || len(ms) != 1 || ms[0].ID != "live" {
		t.Fatalf("models %+v, %v", ms, err)
	}
	plan, _ := s.Plan()
	t.Logf("plan: %+v", plan)
	if plan.ContextWindow == 0 {
		t.Error("no context window reported")
	}
	if !strings.HasPrefix(plan.FitLine, "fit:") {
		t.Logf("no fit line captured for an admin load (goinfer may print the banner only for -model loads)")
	}
	now := time.Now().Add(90 * time.Second)
	s.now = func() time.Time { return now }
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if child, loaded := s.Running(); !child || loaded {
		t.Fatalf("after idle unload: child %v loaded %v", child, loaded)
	}
}
