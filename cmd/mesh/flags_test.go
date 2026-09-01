package main

import (
	"flag"
	"reflect"
	"testing"
	"time"
)

func newAskFlags() (*flag.FlagSet, *time.Duration, *bool) {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 5*time.Minute, "")
	js := fs.Bool("json", false, "")
	return fs, timeout, js
}

// Flags written after the peer name used to be swallowed into the question,
// so `--timeout 3m` silently became part of the prompt and the default timeout
// was used instead.
func TestFlagsAfterPositionalAreParsed(t *testing.T) {
	fs, timeout, js := newAskFlags()
	if err := fs.Parse(hoistFlags(fs, []string{"opencode", "--timeout", "3m", "--json", "how", "are", "you"})); err != nil {
		t.Fatal(err)
	}
	if *timeout != 3*time.Minute {
		t.Errorf("timeout not applied: %v", *timeout)
	}
	if !*js {
		t.Error("bool flag after the peer was not applied")
	}
	if want := []string{"opencode", "how", "are", "you"}; !reflect.DeepEqual(fs.Args(), want) {
		t.Errorf("positionals wrong: %v want %v", fs.Args(), want)
	}
}

func TestFlagsBeforePositionalStillWork(t *testing.T) {
	fs, timeout, _ := newAskFlags()
	fs.Parse(hoistFlags(fs, []string{"--timeout=90s", "opencode", "hello"}))
	if *timeout != 90*time.Second {
		t.Errorf("timeout not applied: %v", *timeout)
	}
	if want := []string{"opencode", "hello"}; !reflect.DeepEqual(fs.Args(), want) {
		t.Errorf("positionals wrong: %v want %v", fs.Args(), want)
	}
}

// A question is free text and may contain dashes; only tokens this command
// actually defines as flags may be hoisted.
func TestUnknownDashTokenStaysInTheQuestion(t *testing.T) {
	fs, _, _ := newAskFlags()
	fs.Parse(hoistFlags(fs, []string{"opencode", "why", "is", "-v", "failing"}))
	if want := []string{"opencode", "why", "is", "-v", "failing"}; !reflect.DeepEqual(fs.Args(), want) {
		t.Errorf("question was mangled: %v want %v", fs.Args(), want)
	}
}

func TestDoubleDashEndsFlagParsing(t *testing.T) {
	fs, timeout, _ := newAskFlags()
	fs.Parse(hoistFlags(fs, []string{"opencode", "--", "--timeout", "is", "a", "flag"}))
	if *timeout != 5*time.Minute {
		t.Errorf("a flag after -- was applied: %v", *timeout)
	}
	if want := []string{"opencode", "--timeout", "is", "a", "flag"}; !reflect.DeepEqual(fs.Args(), want) {
		t.Errorf("positionals wrong: %v want %v", fs.Args(), want)
	}
}
