package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"9fans.net/go/plumb"
)

func TestExpand(t *testing.T) {
	ttab := []struct {
		name, in, out string
	}{
		{"src", "$src", "source"},
		{"Complex regex", "'([.a-zA-Z¡-￿0-9_/\\-@]*[a-zA-Z¡-￿0-9_/\\-])('$addr')?'", "([.a-zA-Z¡-￿0-9_/\\-@]*[a-zA-Z¡-￿0-9_/\\-])(:43)?"},
		{"double quote", "quote '' quote", "quote  quote"},
		{"quoted quote", "quote ' '' ' quote", "quote  '  quote"},
		{"untransformed", "asdf", "asdf"},
		{"numeric expansion", "test $0 bar", "test amatch bar"},
		{"simple quote", "simple 'qu$src' quote", "simple qu$src quote"},
		{"end of line quote", "simple 'qu$src'", "simple qu$src"},
	}
	setvariable("addr", ":43", ":43")
	e := &Exec{
		msg: &plumb.Message{
			Src:  "source",
			Dst:  "destination",
			Dir:  "/tmp",
			Type: "type",
			Attr: &plumb.Attribute{Name: "name", Value: "value"},
			Data: []byte("hello"),
		},
		match: [10]string{"amatch", "", "", "", "", "", "", "", "", ""},
		file:  "filename",
		dir:   "/tmp",
	}
	for _, tc := range ttab {

		got := expand(e, []rune(tc.in))
		if got != tc.out {
			t.Errorf("%s: expected '%v', got '%v'", tc.name, tc.out, got)
		}
	}
}

var fileaddr = `addrelem='((#?[0-9]+)|(/[A-Za-z0-9_\^]+/?)|[.$])'
addr=:($addrelem([,;+\-]$addrelem)*)

twocolonaddr = ([0-9]+)[:.]([0-9]+)
`

func TestReadRules(t *testing.T) {
	r := strings.NewReader(fileaddr)

	rules := newRules()
	err := rules.readrules("fileaddr", r)
	if err != nil {
		t.Errorf("Failed to read fileaddr: %v", err)
	}
	if len(rules.rs) > 0 {
		t.Errorf("fileaddr should not have made any rules")
	}

	bf, err := os.Open("../../../plumb/basic")
	if err != nil {
		t.Fatalf("Failed to open test data")
	}
	rules = newRules()
	err = rules.readrules("basic", bf)
	if err != nil {
		t.Errorf("Failed to read fileaddr: %v", err)
	}
	if len(rules.rs) < 1 {
		t.Errorf("basic should declare some rules")
	}
	bf.Close()

	bf, err = os.Open("../../../plumb/basic")
	if err != nil {
		t.Fatalf("Failed to open test data")
	}
	fsys := NewFsys()
	data, err := io.ReadAll(bf)
	if err != nil {
		t.Fatalf("Failed to read test data")
	}
	err = fsys.writerules(data)
	if err != nil {
		t.Fatalf("Failed to add rules")
	}
	for fsys.text != nil { // Gross.  But in the test the last item is fully formed.
		fsys.writerules(nil)
	}

	if len(fsys.rules.rs) != len(rules.rs) {
		t.Errorf("writerules didn't add the same rules as readrules")
	}
}
