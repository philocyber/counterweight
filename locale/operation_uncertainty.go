package locale

func init() {
	Register("Shell execution is not confined by input rules; human approval is required", "Shell execution is not confined by input rules; human approval is required")
	Register("The shell program cannot be inspected before execution; human approval is required", "The shell program cannot be inspected before execution; human approval is required")
	Register("Request method depends on runtime arguments or configuration; human approval is required", "Request method depends on runtime arguments or configuration; human approval is required")
	Register("The command selects its executable at runtime; human approval is required", "The command selects its executable at runtime; human approval is required")
	Register("Monitor is not available; run the command with Bash and run_in_background set to true, then read it with TaskOutput", "Monitor is not available; run the command with Bash and run_in_background set to true, then read it with TaskOutput")
	Register("Tool %s is not unlocked in this session; load the skill that provides it first", "Tool %s is not unlocked in this session; load the skill that provides it first")
}
