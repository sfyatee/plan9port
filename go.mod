module github.com/sfyatee/plan9port

go 1.25.3

require (
	9fans.net/go v0.0.7
	github.com/PlakarKorp/go-daemonize v0.0.0
)

replace github.com/PlakarKorp/go-daemonize => ./src/cmd/plumb/go-daemonize
