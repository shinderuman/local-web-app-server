package main

import (
	"fmt"
	"os"

	"github.com/shinderuman/local-web-app-server/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "local-web-app-server:", err)
		os.Exit(1)
	}
}
