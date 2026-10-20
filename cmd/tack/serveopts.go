package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// serveOptions are the goinfer-serve options the pilot passes through, by name, and none other (owner, 2026-10-18): a
// run's server differs from the registered one only in ways named here, and each run's record lists the ones it used.
// Each name stands for fixed goinfer-serve flags; a name takes no value of its own.
var serveOptions = map[string][]string{
	// a mixture-of-experts model larger than the card streams its experts (gpt-oss-20b, Qwen3.6-35B-A3B)
	"moe-cache-experts": {"-moe-cache-experts"},
	// thinking off as the server default, beside the thinking disabled tack sends on every request (calibration C4, C5)
	"thinking-off": {"-thinking", "off"},
}

// serveOptionList is the repeatable -serve-option flag. A name not in serveOptions is refused when the flag is parsed,
// so nothing starts. Leading dashes are dropped and a repeat is kept once.
type serveOptionList []string

func (l *serveOptionList) String() string { return strings.Join(*l, ",") }

func (l *serveOptionList) Set(v string) error {
	v = strings.TrimLeft(v, "-")
	if _, ok := serveOptions[v]; !ok {
		var names []string
		for n := range serveOptions {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("goinfer-serve option %q is not passed through; allowed: %s", v, strings.Join(names, ", "))
	}
	for _, o := range *l {
		if o == v {
			return nil
		}
	}
	*l = append(*l, v)
	return nil
}

// args is the list as goinfer-serve's own flags.
func (l serveOptionList) args() []string {
	var out []string
	for _, o := range l {
		out = append(out, serveOptions[o]...)
	}
	return out
}

const serveOptionUsage = "a goinfer-serve option to pass through, by name; repeatable; only: moe-cache-experts, thinking-off"

// settingList is the repeatable -setting flag: [run] settings an A1 arm switches on, each one of pilot.RunSettings,
// refused when the flag is parsed otherwise. A repeat is kept once.
type settingList []string

func (l *settingList) String() string { return strings.Join(*l, ",") }

func (l *settingList) Set(v string) error {
	if !pilot.RunSettings[v] {
		return fmt.Errorf("[run] setting %q is not one an arm may switch on; allowed: %s", v, settingNames())
	}
	for _, s := range *l {
		if s == v {
			return nil
		}
	}
	*l = append(*l, v)
	return nil
}

func settingNames() string {
	var names []string
	for n := range pilot.RunSettings {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
