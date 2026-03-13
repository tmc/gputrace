module github.com/tmc/gputrace

go 1.25.2

require (
	github.com/ebitengine/purego v0.10.0
	github.com/google/pprof v0.0.0-20251007162407-5df77e3f7d1d
	github.com/spf13/cobra v1.10.1
	github.com/spf13/pflag v1.0.9
	github.com/tmc/apple v0.2.2
	github.com/tmc/appledocs/generated v0.0.0-00010101000000-000000000000
	github.com/tmc/macgo v0.0.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
)

replace github.com/tmc/macgo => /Volumes/tmc/go/src/github.com/tmc/macgo

replace github.com/tmc/appledocs => /Volumes/tmc/go/src/github.com/tmc/apledocs-snapshot

replace github.com/tmc/appledocs/generated => /Volumes/tmc/go/src/github.com/tmc/apledocs-snapshot/generated
