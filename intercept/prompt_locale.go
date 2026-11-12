package intercept

import "github.com/Philocyber/counterweight/locale"

func init() {
	locale.Register(JudgeContextBoundary, JudgeContextBoundary)
	locale.Register(JudgeOutputContract, JudgeOutputContract)
	locale.Register(DefaultJudgePrompt, DefaultJudgePrompt)
}
