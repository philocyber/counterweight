# Why Counterweight

The background behind the project's mission. Figures below are quoted from the linked sources.

## The asymmetry

For most of the history of security, a serious attack was expensive. It took skilled people, time
and patience, and that cost quietly protected everyone who was not worth the effort. That
protection is gone.

**Offense now scales like software.** In September 2025 a state-linked group pointed an AI coding
agent at roughly thirty organizations and let it run the operation: reconnaissance, exploitation,
credential harvesting and data exfiltration. The AI performed 80-90% of the campaign, issuing
thousands of requests, often several per second, while humans stepped in at only four to six
decision points.[^anthropic] Not long ago, that operation needed a team.

**The patch window has closed.** Mandiant now estimates the mean time-to-exploit at *minus seven
days*: exploitation routinely begins before a fix exists. Once an intruder is in, access is handed
to the next criminal crew in a median of 22 seconds.[^mtrends] Exploited vulnerabilities have
overtaken stolen credentials as the leading way in, while the median time to fully remediate them
has grown to 43 days.[^dbir-cri][^dbir-helpnet]

**The guardrails are optional.** Frontier models refuse obvious abuse. Open-weight models can be
stripped of their refusals and run anywhere, at almost no cost. Underground mentions of such
modified models rose 745% between January and October 2026, and open-weight capability trails the
frontier by only three to six months.[^flare] Offensive agent frameworks are already traded on
criminal forums with claims of cutting exploitation from days to under ten minutes.[^checkpoint]

**The cost lands on the smallest.** Small organizations made up 96% of ransomware victims in the
2026 Verizon DBIR.[^dbir-cri] They are the ones without a red team, a SOC or a pentest budget. Across
the industry, 59% of security teams report critical or significant skill gaps and 29% cannot afford
to hire the skills they need.[^isc2] The tools that make attacks cheap are spreading downmarket
much faster than the defenses that stop them.

## A weight on the defenders' side

A counterweight is the mass that keeps a lopsided system from tipping over. It does not add force
for its own sake; it restores balance. That is the job this project is named for.

The asymmetry above is not a passing phase. Models will keep getting cheaper and more capable,
guardrails will keep being stripped away, and offense will keep automating. Counterweight does not
pretend to end that imbalance. It exists to push back against it: to put a weight on the
defenders' side of the scale that grows with the same technology attackers use.

Counterweight gives defenders this class of tooling, open source and self-hosted, so a two-person
IT team, a clinic, a school or a growing startup can test its own systems and code with evidence,
and fix what matters before anyone else finds it. Not an annual audit that ages in a drawer, but a
repeatable practice that hardens systems release after release.

[^anthropic]: Anthropic, [Disrupting the first reported AI-orchestrated cyber espionage campaign](https://www.anthropic.com/news/disrupting-AI-espionage), November 2025.
[^mtrends]: Google Cloud / Mandiant, [M-Trends 2026](https://security.googlecloudcommunity.com/google-threat-intelligence-3/announcing-m-trends-2026-data-insights-and-strategies-from-the-frontlines-7123), March 2026.
[^dbir-cri]: Cyber Readiness Institute, [Verizon DBIR 2026: small businesses face escalating cyber threats](https://cyberreadinessinstitute.org/news-and-events/verizon-dbir-2026-small-businesses-face-escalating-cyber-threats/), summary of the 2026 Verizon Data Breach Investigations Report, June 2026.
[^dbir-helpnet]: Help Net Security, [Lessons for organizations from the Verizon 2026 DBIR](https://www.helpnetsecurity.com/2026/05/25/lessons-from-verizon-dbir-2026-findings/), May 2026.
[^flare]: Flare, [AI Models are Fueling a New Wave of Cybercrime: A Quantitative Analysis](https://flare.io/learn/resources/blog/ai-models-cybercrime-quantitative-analysis), October 2026.
[^checkpoint]: Check Point, [Hexstrike-AI: When LLMs Meet Zero-Day Exploitation](https://blog.checkpoint.com/executive-insights/hexstrike-ai-when-llms-meet-zero-day-exploitation/), September 2025. The time reduction is a threat-actor claim reported by Check Point.
[^isc2]: ISC2, [2025 Cybersecurity Workforce Study](https://www.isc2.org/Insights/2025/12/ISC2-publishes-2025-cybersecurity-workforce-study), December 2025.
