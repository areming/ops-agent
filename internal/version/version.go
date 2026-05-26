// Package version holds the build version, injected at build time via
// -ldflags "-X .../internal/version.Value=<tag>". It is read by `ops
// --version` and by enroll to fetch the matching release binary for a new
// host.
package version

// Value is the build version (e.g. "v0.4.2"). It is "dev" for an unversioned
// local build, in which case enroll cannot fetch a release and requires a
// local dist binary instead.
var Value = "dev"
