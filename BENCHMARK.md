# Benchmark

How much does 3gpp-mcp improve LLM accuracy on 3GPP questions, and how much of
that a plain search baseline already reaches. Measured with
[teleqna-eval](https://github.com/higebu/teleqna-eval) on two task sets:
[TeleQnA](https://github.com/netop-team/TeleQnA), and tasks generated from the
specification documents themselves.

Every condition sends the model **the identical prompt, byte for byte**; only
the retrieval differs. Three conditions:

| Condition | What the model gets |
|---|---|
| no tools | the question, nothing else |
| BM25 fixed-k | one full-text search over the same database, top-5 sections prepended, no tools |
| 3gpp-mcp | all 11 MCP tools bridged in, up to 20 tool rounds, the model drives its own search |

Two more conditions exist for the 5G SBI tasks, whose answers live in OpenAPI
documents outside the full-text index: a BM25 search over an index built for
those documents, and an **oracle** handed the exact chunk the question names.
An oracle is not a system anyone can build — it is the ceiling a retriever
could reach if its query were always perfect. A gap that survives it is not a
gap in query quality.

## TeleQnA

All 1,509 `Standards specifications` questions tagged `3GPP`, three repeats of
each condition, on a database pinned as `latest-v2-2026-08-11` (4,129
specifications), served by 3gpp-mcp `09330a0`:

| Model | No tools | With 3gpp-mcp | Δ | 95% CI | Win/loss¹ | McNemar | Calls/q |
|---|---|---|---|---|---|---|---|
| DeepSeek V4 Flash | 74.4% (SD 1.3) | **86.3%** (SD 0.6) | **+12.0pt** | [+10.7, +13.2] | 732 / 191 | χ²=315.9, p<10⁻⁶⁹ | 7.7 |
| Claude Sonnet 5 | 75.1% (SD 0.6) | **84.1%** (SD 0.5) | +8.9pt | [+7.7, +10.1] | 592 / 188 | χ²=208.2, p<10⁻⁴⁶ | 2.1 |
| GPT 5.6 Luna | 73.4% (SD 0.2) | **80.0%** (SD 1.0) | +6.5pt | [+5.4, +7.7] | 511 / 215 | χ²=119.9, p<10⁻²⁷ | 1.3 |

¹ questions only the tools run answered correctly / only the baseline answered correctly.

### Where the gain comes from

Splitting the same measurement against the BM25 baseline separates *reaching
the text at all* from *the model driving its own search*:

| Model | Access: fixed-k − no tools | Agency: 3gpp-mcp − fixed-k |
|---|---|---|
| DeepSeek V4 Flash | +7.8pt (p<10⁻¹⁰) | **+2.6pt** (p=0.005) |
| Claude Sonnet 5 | +9.2pt (p<10⁻¹⁴) | **−0.2pt** (p=0.889) |
| GPT 5.6 Luna | +9.6pt (p<10⁻¹⁴) | **−3.7pt** (p=0.0009) |

Most of the TeleQnA gain is access, and access is model-independent: the three
figures land within 1.8pt of each other. The agency column looks like the
opposite — worth little, and not even stable in sign.

### Both columns are diluted: two models often did not search at all

The fixed-k baseline retrieves on **every** question. The agentic condition
lets the model decide, and two of the three mostly decided not to:

| Model | Searched at least once | Never searched |
|---|---|---|
| DeepSeek V4 Flash | 99.9% | 0.1% |
| Claude Sonnet 5 | 59.6% | **40.4%** |
| GPT 5.6 Luna | 40.0% | **60.0%** |

On a question where nothing was retrieved, the tools condition *is* the
baseline — same prompt, same absent context — so it measures the model, not
3gpp-mcp. The data says exactly that:

| Model | Where it searched | Where it never called a tool |
|---|---|---|
| DeepSeek V4 Flash | 74.3% → **86.3%** (+12.0pt), p<10⁻⁶⁹ | 100% → 100% (n=5) |
| Claude Sonnet 5 | 66.6% → **81.5%** (+14.9pt), p<10⁻⁵⁰ | 87.8% → 87.8% (+0.1pt), p=1 |
| GPT 5.6 Luna | 58.9% → **74.6%** (+15.7pt), p<10⁻²⁹ | 83.1% → 83.5% (+0.4pt), p=0.319 |

Where the tool went unused the two conditions are indistinguishable, which is
what a shared prompt requires and is a check on the protocol: merely attaching
tools changes nothing by itself. **Where the tool was used, the three models
agree to within 3.7pt.** The spread in the headline table — +6.5 to +12.0pt —
is dilution, not a property of the models.

Read the same split against the fixed-k baseline instead, and the agency column
resolves too:

| Model | Where it searched | Where it never called a tool |
|---|---|---|
| DeepSeek V4 Flash | +2.6pt, 109/70, p=0.005 | (n=3) |
| Claude Sonnet 5 | +1.7pt, 84/68, p=0.224 | **−3.2pt**, 17/36, p=0.013 |
| GPT 5.6 Luna | −1.3pt, 63/71, p=0.545 | **−5.3pt**, 46/94, p<10⁻⁴ |

The same BM25 index over the same database performs the same whether it
arrives as a tool result or as a prepended block. The whole apparent deficit is
in the questions answered from memory — 86% of Luna's −3.7pt — where fixed-k
retrieved and the agent did not.

### Asking for retrieval closes it

A prompt variant adds one sentence to the shared prompt — do not answer from
memory, search first — and names no source and no search terms, so it can only
remove the model's discretion over whether to look. On Luna, the model that
skipped most often:

| Run | Accuracy | Never searched |
|---|---|---|
| baseline prompt, no tools | 73.2% | 100% |
| baseline prompt, 3gpp-mcp | 79.1% | 60.6% |
| **search prompt, no tools** | 71.8% | 100% |
| **search prompt, 3gpp-mcp** | **83.8%** | **0.0%** |

Retrieval goes from skipped on 60.6% of questions to none, and the tools
condition gains **+12.0pt** over its own baseline (249/68, p<10⁻²³) instead of
+5.9pt. The sentence itself is worth nothing — with no tools to call it moves
the model −1.5pt (p=0.086) — so the gain is the retrieval, not the wording.

That lands the agentic condition on 83.8% against fixed-k's 82.8%: a difference
of +0.9pt with a 95% interval of [−1.0, +2.9]. Reaching the same index by
tool call is worth what reaching it through a pipeline is worth, to within
three points on 1,509 questions — which is what should happen, since it is the
same index, the same sections and the same BM25. The fixed-k pipeline is one
query; 3gpp-mcp is that query plus the ability to keep going, and TeleQnA is a
question set where one query is usually enough. The next section is where the
rest of it is worth something.

Two cautions on these subsets. They are chosen by the model, not sampled: it
searches when unsure, so the searched subset starts 8-24 points lower and
+12 to +16pt is the effect **on the questions a model thinks it needs help
with**, not on TeleQnA as a whole. And the headline table remains the honest
summary of *offering* the tool, which is what a deployment actually does.

## Specification-grounded tasks

Tasks generated from the specifications themselves, where the answer is a value
in a table, an ASN.1 field, an equation, or a 5G SBI schema, and the model must
also cite the clause that says so. 50 tasks per type (44 and 43 for two of
them), one pass per condition, same database and server.

**Answer correct:**

| Task type | Baseline² | DeepSeek | Sonnet | Luna |
|---|---|---|---|---|
| GTPv2-C codes | 26 / 40 / 40% | **100%** | 96% | 98% |
| PFCP codes | 40 / 56 / 56% | **100%** | 94% | **100%** |
| Diameter AVPs | 70 / 76 / 74% | 96% | **100%** | **100%** |
| ASN.1 structure | 4 / 6 / 6% | 94% | 94% | 90% |
| Equations | 42 / 62 / 32% | 62% | 64% | 48% |
| SBI request body | 100 / 100 / 100% | **100%** | **100%** | **100%** |
| SBI object property | 90 / 94 / 90% | 98% | **100%** | **100%** |
| SBI allOf composition | 95 / 95 / 95% | **100%** | **100%** | 93% |
| SBI oneOf alternatives | 100 / 100 / 98% | **100%** | **100%** | **100%** |

² the strongest non-agentic condition, DeepSeek / Sonnet / Luna: BM25 fixed-k
for the first five types, the oracle for the SBI types.

**Answer correct *and* correctly cited** — the number that decides whether the
answer can be checked:

| Task type | Baseline² | DeepSeek | Sonnet | Luna |
|---|---|---|---|---|
| GTPv2-C codes | 22 / 40 / 24% | **98%** | 96% | 96% |
| PFCP codes | 34 / 48 / 40% | **98%** | 92% | **100%** |
| Diameter AVPs | 70 / 68 / 68% | 96% | **100%** | 96% |
| ASN.1 structure | 4 / 4 / 2% | 88% | **92%** | 90% |
| Equations | 40 / 60 / 28% | 58% | 60% | 44% |
| SBI request body | 98 / 14 / 54% | 94% | **100%** | 98% |
| SBI object property | 84 / 58 / 76% | 98% | **100%** | **100%** |
| SBI allOf composition | 95 / 41 / 84% | 98% | **100%** | 93% |
| SBI oneOf alternatives | 98 / 79 / 86% | 98% | **100%** | **100%** |

Against the BM25 baseline the agentic condition wins by +26 to +88pt on every
protocol-structure type, on every model, with essentially no losing pairs —
ASN.1 is 42-45 wins against 0 losses on all three. Here every model searches,
because none of them can answer these from memory; the policy problem that
flattened the TeleQnA agency column does not arise, and what is left is the
difference retrieval depth makes.

## Takeaways

- **Ask for retrieval and you get the whole effect.** Sonnet skipped searching
  on 40% of TeleQnA questions and Luna on 60%, and a question where nothing was
  retrieved measures the model, not the server. One sentence in the prompt
  takes Luna's skip rate to zero and its gain from +5.9 to +12.0pt.
- **Reaching the index by tool call is worth what reaching it through a
  pipeline is worth** — the two land within three points of each other on
  1,509 questions, as they should: same database, same sections, same BM25.
  A fixed-k pipeline is one query of what 3gpp-mcp can do.
- **What the tool adds beyond a pipeline is depth, and depth is what the
  documents demand.** A TeleQnA question is usually one hop from the first
  retrieved passage, so one query reaches it. Protocol structure is two or more
  hops — a table entry names a type defined elsewhere, a schema references
  another schema — and only the model can follow that itself. That is the
  difference between +2.6pt and +88pt.
- **Retrieving the answer and being able to cite it are different
  capabilities.** The oracle condition is *handed* the chunk that contains the
  answer and answers 94-100% correctly — yet on Sonnet it can attribute that
  answer to the right clause only 14-79% of the time, against 100% when the
  model fetched the document itself.
- **Without retrieval, citations are often fabricated.** Over the 250
  clause-text tasks, a model with no tools names a clause that does not exist
  42-143 times depending on the model; with 3gpp-mcp, 2-4.
- **Equations are the one area where the tool genuinely adds nothing.** Sonnet
  reads 62% from a single BM25 query and 64% with tools (5 wins / 4 losses,
  p=1) — and here it did search, on every task. Formulas sit in the retrieved
  section already; there is nothing to follow.
- **38 of 1,509 TeleQnA questions were answered correctly by all three models
  with tools and by none of them without** — and 6 in the opposite direction.

## Method

- Both conditions of every pair send the identical prompt, reproducing
  TeleQnA's own; a test fails the build if they ever diverge. The superseded
  measurement used a different prompt per condition, which inflated the gain to
  a uniform ≈+11pt across models. The search variant is a second registered
  prompt, appended to that same text and sent to both conditions alike, so the
  pair it forms is symmetric on its own terms; it is never compared with a run
  of the other prompt as if the tools were the only difference.
- Same 1,509-question set for every run; questions whose text lacks `3GPP`
  (IEEE 802.11 etc.) are excluded from the category.
- Each model on its vendor's own API: `api.deepseek.com` (DeepSeek V4 Flash),
  Anthropic Messages (Claude Sonnet 5), OpenAI Responses (GPT 5.6 Luna).
- No token limit, 20 tool rounds, answers scored by exact option match,
  unanswered counts as wrong. A request that failed at the transport is not an
  answer and is re-run, never scored.
- **One deviation between models**: DeepSeek sent `temperature 0`; Sonnet and
  Luna reject a non-default sampling parameter, so their requests carry none
  and the provider default applies.
- The database is pinned by manifest and SHA-256, and every run records the
  harness commit, the flags, the prompt hash, the MCP server identity and the
  database identifier.

## What this does not measure

- The search variant ran on one model. Luna is the one that skipped retrieval
  most, so it had the most to gain; whether one sentence recovers as much on a
  model that already searches on 60% of questions is untested.
- One prompt format. TeleQnA is multiple choice; the specification-grounded
  tasks are short-answer with a required citation. Neither is a working
  engineer's question.
- n=50 per task type, one pass per condition on that set — enough for the
  +24 to +88pt differences, not for the small ones.
- The specification-grounded tasks are generated from the same database the
  tools read. They test whether the model can find and cite what is there, not
  whether the corpus is right.
- Absolute numbers are not comparable to Telco-RAG / TelcoAI / GSMA leaderboard
  figures (different models, prompt formats and subsets); the paired same-model
  delta is the measurement.

Harness, full methodology and reproduction scripts:
[higebu/teleqna-eval](https://github.com/higebu/teleqna-eval).
