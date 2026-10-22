// Command herdmux is the herdmux CLI. It holds the thin verbs only; the work
// lives in internal/.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], &env{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		getenv: os.Getenv,
		ppid:   os.Getppid(),
	}))
}
