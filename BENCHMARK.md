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
figures land within 1.8pt of each other. What the model does with the tools
afterwards is worth little here, and its sign is not even stable across models.

That is a fact about the questions, not about the tools. A TeleQnA question is
usually answerable from one retrieved passage, so a single search reaches most
of what there is to reach.

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
ASN.1 is 42-45 wins against 0 losses on all three. **The effect TeleQnA showed
as small and unstable in sign is large and consistent here.**

## Takeaways

- **The value of a search tool depends on how far the answer sits from the
  first retrieved chunk.** TeleQnA questions are mostly one hop away, so one
  BM25 query captures most of the gain. Questions about protocol structure are
  two or more hops away — a table entry names a type defined elsewhere, a
  schema references another schema — and there the model has to follow the
  reference itself. That is the whole difference between +2.6pt and +88pt.
- **Retrieving the answer and being able to cite it are different
  capabilities.** The oracle condition is *handed* the chunk that contains the
  answer and answers 94-100% correctly — yet on Sonnet it can attribute that
  answer to the right clause only 14-79% of the time, against 100% when the
  model fetched the document itself.
- **Without retrieval, citations are often fabricated.** Over the 250
  clause-text tasks, a model with no tools names a clause that does not exist
  42-143 times depending on the model; with 3gpp-mcp, 2-4.
- **Equations are the one area where the tool does not help.** Sonnet reads
  62% from a single BM25 query and 64% with tools (5 wins / 4 losses, p=1).
  Formulas sit in the retrieved section already; there is nothing to follow.
- **38 of 1,509 TeleQnA questions were answered correctly by all three models
  with tools and by none of them without** — and 6 in the opposite direction.

## Method

- Both conditions of every pair send the identical prompt, reproducing
  TeleQnA's own; a test fails the build if they ever diverge. The superseded
  measurement used a different prompt per condition, which inflated the gain to
  a uniform ≈+11pt across models.
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
