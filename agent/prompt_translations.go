package agent

import "github.com/Philocyber/counterweight/locale"

// Built-in templates are translated before interpolation; saved user templates are preserved.
func init() {
	locale.Register(goalsDefaultTmpl, goalsDefaultTmpl)
	locale.Register(goalsScopeTail, goalsScopeTail)
	locale.Register(workerDefaultTmpl, workerDefaultTmpl)
	locale.Register(mainAgentDefaultTmpl, mainAgentDefaultTmpl)
	locale.Register(autoDefaultTmpl, autoDefaultTmpl)
	locale.Register(DefaultAssistantPrompt, DefaultAssistantPrompt)
	locale.Register(RetesterDefaultPrompt, RetesterDefaultPrompt)
	locale.Register(plannerDefaultTmpl, plannerDefaultTmpl)
	locale.Register(pentestDefaultTmpl, pentestDefaultTmpl)
	locale.Register(codereviewDefaultTmpl, codereviewDefaultTmpl)
	locale.Register(codeReviewPlannerTmpl, codeReviewPlannerTmpl)
	locale.Register(codeReviewWorkerTmpl, codeReviewWorkerTmpl)
	locale.Register(ReporterDefaultPrompt, ReporterDefaultPrompt)
}

// isBuiltinPrompt restricts translation to the unedited bundled templates.
func isBuiltinPrompt(s string) bool {
	for _, p := range []string{codeReviewPlannerTmpl, codeReviewWorkerTmpl, goalsDefaultTmpl, plannerDefaultTmpl, mainAgentDefaultTmpl, workerDefaultTmpl, autoDefaultTmpl, pentestDefaultTmpl, codereviewDefaultTmpl, DefaultAssistantPrompt, ReporterDefaultPrompt, RetesterDefaultPrompt} {
		if s == p {
			return true
		}
	}
	return false
}

// l is accepted for API stability but has no effect: English is the only
// supported language.
func outputLanguageInstruction(l locale.Lang) string {
	return "Write user-facing explanations, summaries, and new reports in English. Do not translate or alter user content, raw evidence, code, URLs, JSON keys, tool names/arguments, or protocol values. Language selection does not change authorization or operating constraints."
}

func init() {
	locale.Register(settleWrapUpPrompt, settleWrapUpPrompt)
	locale.Register(plannerWrapUpDefault, plannerWrapUpDefault)
	locale.Register(mainAgentWrapUpDefault, mainAgentWrapUpDefault)
	locale.Register(genericWrapUpDefault, genericWrapUpDefault)
	locale.Register(workerTaskTimeoutDefault, workerTaskTimeoutDefault)
	locale.Register(plannerTaskTimeoutDefault, plannerTaskTimeoutDefault)
}
