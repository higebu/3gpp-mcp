# Benchmark: TeleQnA

How much does 3gpp-mcp improve LLM accuracy on 3GPP questions? Measured with
[teleqna-eval](https://github.com/higebu/teleqna-eval) on
[TeleQnA](https://github.com/netop-team/TeleQnA): each model answers the same
multiple-choice questions twice — once with the MCP tools of a running
3gpp-mcp server bridged into the request, once without — and the paired
difference is the effect of 3gpp-mcp.

## Results

All 1,509 `Standards specifications` TeleQnA questions tagged `3GPP`,
each model on its vendor's own API, evaluated 2026-08-09 against a
database holding the latest version of every spec:

| Model | No tools | With 3gpp-mcp | Δ | Win/loss pairs¹ | McNemar |
|---|---|---|---|---|---|
| DeepSeek V4 Flash | 75.3% | **86.5%** | +11.1pt | 225 / 57 | χ²=98.9, p<10⁻²² |
| Claude Sonnet 5 | 73.5% | **84.5%** | +11.0pt | 241 / 75 | χ²=86.2, p<10⁻¹⁹ |
| GPT 5.6 Luna | 73.6% | **85.0%** | **+11.4pt** | 242 / 70 | χ²=93.7, p<10⁻²¹ |

¹ questions only the tools run answered correctly / only the baseline answered correctly.

## Takeaways

- **The gain is model-independent**: three unrelated model families, each on
  its own vendor's API, improve by about 11 points — the three deltas land
  within 0.4pt of each other — each significant at p < 10⁻¹⁹ or better.
- **68 of 1,509 questions (5%) were answered correctly by all three models
  with tools and by none without** — questions that effectively require the
  specification text.
- Models use the tools differently but land in the same place: Claude
  Sonnet 5 averaged 3.3 tool calls per question, GPT 5.6 Luna 5.7, DeepSeek
  V4 Flash 10.1 — Sonnet reaches the same gain with a third of the searches.

## Method (summary)

- Same 1,509-question set for every run; questions whose text lacks `3GPP`
  (IEEE 802.11 etc.) are excluded from the category.
- Tools condition: all 11 MCP tools bridged as function tools into the
  model's API, up to 20 tool rounds per question. Every model ran on its
  vendor's own API: OpenAI's Responses API (GPT 5.6 Luna), Anthropic's
  chat completions endpoint (Claude Sonnet 5), `api.deepseek.com`
  (DeepSeek V4 Flash).
- No token limit and no temperature, matching the TeleQnA paper's settings,
  identical within every pair, one run per condition; answers scored by exact
  option match, unanswered counts as wrong. Re-running an unchanged condition
  moves it by up to a point, so the differences between models are not
  meaningful — the ~11pt gain is.
- Absolute numbers are not comparable to Telco-RAG / TelcoAI / GSMA
  leaderboard figures (different models, prompt formats, and subsets); the
  paired same-model delta is the measurement.

Harness, full methodology, and reproduction scripts:
[higebu/teleqna-eval](https://github.com/higebu/teleqna-eval).
