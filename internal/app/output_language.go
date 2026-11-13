package app

// Apply this rule at the shared dispatch point, including resumed sessions.
const agentOutputLanguageInstruction = `

Required output language for every agent and every run: Use ASD-STE100 Simplified Technical English for all natural-language text that you write. This rule applies to tasks and task descriptions, comments, logs, progress reports, final responses, session summaries, shared memories, documentation, code comments, commit messages, pull request (PR) titles and descriptions, release notes, and all other artifacts. Apply this rule to new text and text that you change, even when the input or earlier session uses a different style. Include this rule in instructions that you give to other agents.
Use short, clear sentences and approved words with consistent meanings. Use active voice and give one instruction in each sentence. Avoid idioms, jargon, and complex sentence structures. Keep required technical names and terms. Do not change code, commands, identifiers, paths, URLs, API fields, exact quotations, or raw tool output only to change their language. Write your explanations of these items in Simplified Technical English. Check your text against this rule before you publish it. Do not claim that a formal ASD-STE100 compliance check passed unless you performed that check.
`
