// Command gkebnkctl is the user-facing CLI entrypoint.
//
// All command logic lives in internal/cli; this file just hands off so the
// cli package stays importable for tests.
package main

import "github.com/JLCode-tech/gkebnkctl/internal/cli"

func main() {
	cli.Execute()
}
