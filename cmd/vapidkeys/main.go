// Command vapidkeys prints a new VAPID key pair for Web Push.
//
//	go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest
//	go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest -format json
//
// The default output is two lines for an environment file. Keep the private
// key secret and generate the pair once: browsers subscribe with the public
// key, so a new pair means every browser has to subscribe again.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ez-technologies/webpush"
)

func main() {
	format := flag.String("format", "env", "output format: env or json")
	prefix := flag.String("prefix", "VAPID_", "variable name prefix for -format env")
	flag.Parse()
	if err := run(os.Stdout, *format, *prefix); err != nil {
		fmt.Fprintln(os.Stderr, "vapidkeys:", err)
		os.Exit(1)
	}
}

func run(w io.Writer, format, prefix string) error {
	keys, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	switch format {
	case "env":
		_, err = fmt.Fprintf(w, "%sPUBLIC_KEY=%s\n%sPRIVATE_KEY=%s\n", prefix, keys.Public, prefix, keys.Private)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		err = enc.Encode(keys)
	default:
		err = fmt.Errorf("unknown format %q (use env or json)", format)
	}
	return err
}
