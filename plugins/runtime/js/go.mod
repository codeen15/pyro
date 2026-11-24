module github.com/codeen15/pyro/plugins/runtime/js

go 1.27.3

require (
	github.com/codeen15/pyro/plugins/sdk/go v0.0.0
	github.com/dop251/goja v0.0.0-20261031103838-cf2ff0ec0b31
)

require (
	github.com/dlclark/regexp2/v2 v2.11.0 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/codeen15/pyro/plugins/sdk/go => ../../sdk/go
