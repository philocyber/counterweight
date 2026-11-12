package locale

func init() {
	Register("Task budget reached", "Task budget reached")
	Register("%w (%s). Raise the task budget to continue", "%w (%s). Raise the task budget to continue")
	Register("%d of %d tokens used", "%d of %d tokens used")
	Register("%.4f of %.4f %s estimated", "%.4f of %.4f %s estimated")
	Register("Budget warning: %s (%d%% of the budget used)", "Budget warning: %s (%d%% of the budget used)")
	Register("Budget reached: %s", "Budget reached: %s")
	Register("Budget reached: %s. The task is paused. Raise the budget and resume to continue.", "Budget reached: %s. The task is paused. Raise the budget and resume to continue.")
	Register("The task budget was reached (%s). The task is paused; raise the budget and resume to continue.", "The task budget was reached (%s). The task is paused; raise the budget and resume to continue.")
	Register("Provide at least one of token_budget, cost_budget or cost_currency", "Provide at least one of token_budget, cost_budget or cost_currency")
	Register("Budget values must be zero or positive numbers", "Budget values must be zero or positive numbers")
	Register("Currency must be a three-letter code such as USD", "Currency must be a three-letter code such as USD")
}
