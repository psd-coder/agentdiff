//go:build !darwin

package agent

func sandboxed(_, bin string, args []string) (string, []string) { return bin, args }
