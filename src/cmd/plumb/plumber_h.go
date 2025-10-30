package main

import (
	"regexp"

	"9fans.net/go/plan9"
	"9fans.net/go/plumb"
)

type Object int

const (
	OArg = Object(iota)
	OAttr
	OData
	ODst
	OPlumb
	OSrc
	OType
	OWdir
)

type Verb int

const (
	VAdd = Verb(iota) /* apply to OAttr only */
	VClient
	VDelete /* apply to OAttr only */
	VIs
	VIsdir
	VIsfile
	VMatches
	VSet
	VStart
	VTo
)

type Rule struct {
	obj   Object
	verb  Verb
	arg   string /* unparsed string of all arguments */
	qarg  string /* quote-processed arg string */
	regex *regexp.Regexp
}

type Rules struct {
	rs    []*Ruleset
	ports []string
	dir   []*Dirtab
}

func newRules() Rules {
	return Rules{[]*Ruleset{}, []string{},
		[]*Dirtab{
			&Dirtab{name: ".", typ: plan9.QTDIR, qid: Qdir, perm: 0o500 | plan9.DMDIR, readq: nil, sendq: nil},
			&Dirtab{name: "rules", typ: plan9.QTFILE, qid: Qrules, perm: 0o600, readq: nil, sendq: nil},
			&Dirtab{name: "send", typ: plan9.QTFILE, qid: Qsend, perm: 0o200, readq: nil, sendq: nil}}}
}

type Ruleset struct {
	pat  []Rule
	act  []Rule
	port string
}

type Exec struct {
	msg           *plumb.Message
	match         [10]string
	text          string
	p0, p1        int
	clearclick    bool
	setdata       bool
	holdforclient bool
	file          string
	dir           string
}
