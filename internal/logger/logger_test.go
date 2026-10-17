package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestInitLevelsAndFormats(t *testing.T) {
	cases := []struct {
		level  string
		format string
		method string
	}{
		{"debug", "json", "Debug"},
		{"info", "text", "Info"},
		{"warn", "json", "Warn"},
		{"error", "text", "Error"},
		{"INFO", "TEXT", "Info"},
		{"", "", "Info"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		Init(c.level, c.format, &buf)
		switch c.method {
		case "Debug":
			Debug("hello", "k", "v")
		case "Warn":
			Warn("hello", "k", "v")
		case "Error":
			Error("hello", "k", "v")
		default:
			Info("hello", "k", "v")
		}
		if buf.Len() == 0 {
			t.Fatalf("no output for level=%q format=%q", c.level, c.format)
		}
		if strings.ToLower(c.format) == "json" && !strings.Contains(buf.String(), `"msg"`) {
			t.Fatalf("expected json for format json, got %q", buf.String())
		}
		if got := L(); got == nil {
			t.Fatal("L() nil")
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	Init("info", "text", &buf)
	Debug("hidden")
	Info("shown")
	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Fatal("debug should be suppressed at info level")
	}
	if !strings.Contains(out, "shown") {
		t.Fatal("info should appear at info level")
	}
}

func TestInitNilWriter(t *testing.T) {
	Init("info", "text", nil)
	if L() == nil {
		t.Fatal("nil logger")
	}
}

func TestConvenienceFuncs(t *testing.T) {
	var buf bytes.Buffer
	Init("debug", "json", &buf)
	Debug("dmsg")
	Info("imsg")
	Warn("wmsg")
	Error("emsg")
	out := buf.String()
	for _, want := range []string{"dmsg", "imsg", "wmsg", "emsg"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}
