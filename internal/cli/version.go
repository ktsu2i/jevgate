package cli

// version is the version reported by --version. Development builds report
// "dev"; release builds override it at link time with
//
//	go build -ldflags "-X github.com/ktsu2i/jevgate/internal/cli.version=v0.1.0" ./cmd/jevgate
//
// The release configuration must set this same variable path.
var version = "dev"
