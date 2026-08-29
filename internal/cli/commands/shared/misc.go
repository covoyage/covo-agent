package shared

// PrependArg prepends a subcommand head to raw cobra args.
func PrependArg(head string, args []string) []string {
	out := make([]string, 0, len(args)+1)
	out = append(out, head)
	out = append(out, args...)
	return out
}
