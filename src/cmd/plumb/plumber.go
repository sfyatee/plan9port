package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

var debug = flag.Bool("d", false, "enable debuging output")
var plumbfile = flag.String("p", "", "path of plumbing file")

func main() {
	flag.Parse()
	if *plumbfile == "" {
		*plumbfile = os.Getenv("HOME") + "/lib/plumbing"
		if _, err := os.Stat(*plumbfile); err != nil {
			*plumbfile = os.Getenv("PLAN9") + "/plumb/initial.plumbing"
		}
	}

	f, err := os.Open(*plumbfile)
	if err != nil {
		errorf("can't open rules file %s: %v", plumbfile, err)
	}

	fsys := NewFsys()
	err = fsys.rules.readrules(*plumbfile, f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	fsys.rules.makeports()
	f.Close()
	fsys.start()
}

func errorf(format string, args ...interface{}) {
	if *debug {
		fmt.Fprintf(os.Stderr, "Debug: "+format, args...)
	}
}

type ParseError struct {
	Message string
	Err     *ParseError
	Line    int
	File    string
}

func (e ParseError) Error() string {
	s := fmt.Sprintf("%s:%d %s", e.File, e.Line, e.Message)
	if e.Err != nil {
		s = s + fmt.Sprintf("\n\t%s", e.Err.Error())
	}
	return s
}

func (i *Input) NewError(fm string, args ...any) *ParseError {
	e := ParseError{
		Message: fmt.Sprintf(fm, args...),
		File:    i.file,
		Line:    i.lineno,
	}
	if i.next != nil {
		e.Err = i.next.NewError("%s", "\tat ")
	}
	return &e
}

func unsharp(s string) string {
	if s[0:3] == "#9/" {
		p9 := os.Getenv("PLAN9")
		if p9 != "" {
			return filepath.Join(p9, s[3:])
		}
	}
	return s
}
