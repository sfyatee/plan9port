package main

import (
	"bytes"
	"testing"

	"9fans.net/go/plumb"
)

func TestPlumbline(t *testing.T) {
	ttab := []struct {
		name     string
		input    string
		line     string
		consumed int
	}{
		{"single line", "line\n", "line", 5},
		{"two lines", "line1\nline2", "line1", 6},
		{"no line", "noline", "", 0},
		{"empty", "", "", 0},
	}

	for _, tc := range ttab {
		binput := []byte(tc.input)
		if tc.input == "" {
			binput = nil
		}
		line, consumed := plumbline(binput)
		if consumed != tc.consumed {
			t.Errorf("%s: bad consumed. Got '%d' expected '%d'", tc.name, consumed, tc.consumed)
		}
		if line != tc.line {
			t.Errorf("%s: bad line. Got '%s' expected '%s'", tc.name, line, tc.line)
		}
	}
}

func TestUnpackPartial(t *testing.T) {
	attr := &plumb.Attribute{
		Name:  "addr",
		Value: "/root/",
	}
	message := &plumb.Message{
		Src:  "plumb",
		Dst:  "edit",
		Dir:  "/Users/r",
		Type: "text",
		Attr: attr,
		Data: []byte("/etc/passwd"),
	}
	b := bytes.Buffer{}
	message.Send(&b)
	msg := b.Bytes()

	m, i := unpackPartial(msg[0:48])
	if m != nil {
		t.Errorf("Expected a short read")
	}
	if i != 3 {
		t.Errorf("Got %d for i", i)
	}
	m, i = unpackPartial(msg[0 : 48+i])
	if string(m.Data) != "/etc/passwd" {
		t.Errorf("Incorrect message %#v, i: %v\n", m, i)
	}
}
