package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/proto"
)

// TestStartModeIsTheFlagThenTheOwnersConfigThenPlan: -mode wins over [session] mode, which wins over the default, plan; a value
// that is not a mode is an error whichever gave it.
func TestStartModeIsTheFlagThenTheOwnersConfigThenPlan(t *testing.T) {
	cfg := func(m string) *config.Config { return &config.Config{Session: config.Session{Mode: m}} }
	for _, c := range []struct {
		flag string
		cfg  *config.Config
		want proto.Mode
	}{
		{"", cfg(""), proto.ModePlan},
		{"", cfg("auto"), proto.ModeAuto},
		{"ask", cfg("auto"), proto.ModeAsk},
		{"edits", cfg(""), proto.ModeEdits},
		{"plan", cfg("auto"), proto.ModePlan},
	} {
		got, err := startMode(c.flag, c.cfg)
		if err != nil || got != c.want {
			t.Errorf("flag %q, config %q: %q, %v; want %s", c.flag, c.cfg.Session.Mode, got, err, c.want)
		}
	}
	for _, bad := range []string{"yolo", "AUTO", " ask"} {
		if _, err := startMode(bad, cfg("")); err == nil {
			t.Errorf("flag %q accepted", bad)
		}
	}
}

// TestConfigAndProtocolAgreeOnWhatAModeIs: the config package's list of modes is proto's, so a mode added to one and not the
// other fails here.
func TestConfigAndProtocolAgreeOnWhatAModeIs(t *testing.T) {
	for _, m := range []proto.Mode{proto.ModePlan, proto.ModeAsk, proto.ModeEdits, proto.ModeAuto} {
		if !config.ValidMode(string(m)) {
			t.Errorf("config rejects %q", m)
		}
	}
	if config.ValidMode("") || config.ValidMode("yolo") {
		t.Error("config accepts a non-mode")
	}
}

// TestTheModeFlagIsCheckedBeforeAnythingStarts: a bad -mode, or one with anything after it that the screen does not take, is a
// usage error that names the problem, and so is -mode where there is no terminal; none of them dials the daemon.
func TestTheModeFlagIsCheckedBeforeAnythingStarts(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-mode", "yolo"}, "mode"},
		{[]string{"--mode=yolo"}, "mode"},
		{[]string{"-mode"}, "mode"},
		{[]string{"-mode", "ask", "extra"}, "usage"},
		{[]string{"-mode", "ask"}, "terminal"},
	} {
		var out, errs bytes.Buffer
		if code := dispatch(c.args, &out, &errs); code != exitUsage || !strings.Contains(errs.String(), c.want) {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errs.String())
		}
	}
}
