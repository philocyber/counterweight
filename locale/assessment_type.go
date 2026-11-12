package locale

func init() {
	Register("Repository fields require code_review", "Repository fields require code_review")
	Register("Code reviews do not accept inherited tasks", "Code reviews do not accept inherited tasks")
	Register(" Additional operator instructions: ", " Additional operator instructions: ")
	Register("A code review needs a repository", "A code review needs a repository")
	Register("Cloned, but could not create the review task: %w", "Cloned, but could not create the review task: %w")
	Register("Code review: ", "Code review: ")
	Register("Code reviews do not accept network scope, inherited tasks, interception rules or seed_first_intent", "Code reviews do not accept network scope, inherited tasks, interception rules or seed_first_intent")
	Register("Conflicting repository refs", "Conflicting repository refs")
	Register("Could not clone %s: %w", "Could not clone %s: %w")
	Register("Finish the task before reclassifying historical records", "Finish the task before reclassifying historical records")
	Register("Only legacy general tasks can be reclassified", "Only legacy general tasks can be reclassified")
	Register("Reclassification is only for tasks that already ran; change the type without reclassify", "Reclassification is only for tasks that already ran; change the type without reclassify")
	Register("Repository fields are accepted only when changing to code_review", "Repository fields are accepted only when changing to code_review")
	Register("The assessment type can be changed only before the task starts", "The assessment type can be changed only before the task starts")
	Register("assessment_type must be general, web_app, network or code_review", "assessment_type must be general, web_app, network or code_review")
	Register("migrate code review target of task %d: %w", "migrate code review target of task %d: %w")
}
