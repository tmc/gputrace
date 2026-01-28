module github.com/tmc/gputrace

go 1.25.2

require (
	github.com/ebitengine/purego v0.9.1
	github.com/google/pprof v0.0.0-20251007162407-5df77e3f7d1d
	github.com/spf13/cobra v1.10.1
	github.com/tmc/appledocs/generated v0.0.0-00010101000000-000000000000
	github.com/tmc/macgo v0.0.0
	howett.net/plist v1.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.39.0 // indirect
)

replace github.com/tmc/macgo => /Volumes/tmc/go/src/github.com/tmc/macgo

replace github.com/tmc/appledocs => /Volumes/tmc/go/src/github.com/tmc/apledocs-snapshot

replace github.com/tmc/appledocs/generated => /Volumes/tmc/go/src/github.com/tmc/apledocs-snapshot/generated
