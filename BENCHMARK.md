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

Two more conditions exist for the 5G SBI tasks. Their answers live in OpenAPI
documents, which this server stored but did not put in an FTS5 index when this
was measured — a choice in 3gpp-mcp, not a fact about search — so the
clause-text fixed-k baseline cannot reach them, and its score on those tasks
says nothing about what a retrieval pipeline can do. The two conditions exist
to remove that artifact: **BM25 over OpenAPI** queries an index built for
exactly these documents, and **oracle lookup** is handed the chunk the question
names. An oracle is not a system anyone can build — it is the ceiling a
retriever could reach if its query were always perfect. On the SBI tasks those
two, not fixed-k, are what 3gpp-mcp is measured against.

That choice has since been reversed. `search_openapi` puts the OpenAPI store
behind its own FTS5 index, which makes twelve tools rather than eleven, and it
was measured as a sixth condition on the SBI tasks — see [Searching the OpenAPI
store](#searching-the-openapi-store-instead-of-navigating-it) below. Every other
number on this page was measured with the eleven-tool set.

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
remove the model's discretion over whether to look. It ran on the model that
skipped most, and on the one that never skipped:

| Run | Luna | DeepSeek V4 Flash |
|---|---|---|
| baseline prompt, no tools | 73.2% | 75.5% |
| baseline prompt, 3gpp-mcp | 79.1% (skipped 60.6%) | 85.9% (skipped 0.2%) |
| search prompt, no tools | 71.8% | 73.9% |
| **search prompt, 3gpp-mcp** | **83.8%** (skipped 0%) | **86.6%** (skipped 0%) |

Luna's retrieval goes from skipped on 60.6% of questions to none, and its tools
condition gains **+12.0pt** over its own baseline (249/68, p<10⁻²³) instead of
+5.9pt. DeepSeek, which already searched on 99.8%, moves +0.7pt [−0.5, +2.0],
p=0.305 — the falsification test: had the sentence lifted a model with nothing
left to force, it would be doing something other than forcing retrieval, and
Luna's +12.0pt would not mean what it appears to.

The sentence itself is worth nothing either way. With no tools to call it moves
Luna −1.5pt (p=0.086) and DeepSeek −1.6pt (p=0.102) — two models agreeing that
being told not to answer from memory, with no way to look anything up, is
slightly worse than not being told. What follows is the retrieval, not the
wording: within the search prompt, attaching the tools is worth +12.0pt on Luna
and +12.7pt on DeepSeek.

Against fixed-k, that puts Luna at 83.8% versus 82.8% — +0.9pt, 95% interval
[−1.0, +2.9] — and DeepSeek at 86.6% versus 83.3%, +3.3pt [+1.6, +5.0],
p=0.0002. Reaching the same index by tool call is worth at least what reaching
it through a pipeline is worth, which is what should happen, since it is the
same index, the same sections and the same BM25. The fixed-k pipeline is one
query; 3gpp-mcp is that query plus the ability to keep going.

### The gain over one query is entirely in the questions that went further

Splitting the agentic condition by which tools it actually called separates
"searched" from "searched and then opened a section":

| Model | Stratum | n | no tools | fixed-k | 3gpp-mcp | vs fixed-k |
|---|---|---|---|---|---|---|
| DeepSeek V4 Flash | search only | 124 | 92.7% | 94.4% | 95.2% | +0.8pt, p=1 |
| | + `get_section` | 1382 | 73.9% | 82.3% | 85.0% | **+2.7pt, p=0.005** |
| Claude Sonnet 5 | search only | 408 | 77.2% | 90.4% | 88.5% | −2.0pt, p=0.302 |
| | + `get_section` | 507 | 57.6% | 70.0% | 74.8% | **+4.7pt, p=0.026** |
| GPT 5.6 Luna³ | search only | 93 | 80.6% | 89.2% | 94.6% | +5.4pt, p=0.131 |
| | + `get_section` | 1416 | 72.7% | 82.4% | 83.1% | +0.6pt, p=0.584 |

³ from the search-prompt run, the one where Luna retrieves on every question.
Sonnet's remaining 594 questions called nothing at all and are the rows in the
previous table.

Where the model stopped at the search result, it did what the pipeline does, and
no model separates from fixed-k there at any usable confidence: the three
measured differences are +0.8, −2.0 and +5.4pt, none significant, and they do
not agree on a sign. Both differences that do reach significance are in the
stratum where the model opened a section, and both favour the tool.

The strata also differ in difficulty exactly as that reading predicts: a
question answered from the search snippet alone is one the model could mostly
answer unaided (77-93% with no tools), and one that sent it into a section
could not be (58-74%). The model is deciding, per question, whether one hop is
enough — and it is on the questions where it is not that the next section's
+26 to +88pt live.

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

### Searching the OpenAPI store instead of navigating it

The two extra conditions above put a purpose-built search over the OpenAPI
store against an agent that navigates it. `search_openapi` makes that a
comparison between two searches over one store: the server's own FTS5 index,
one chunk per schema and per operation with `$ref` expanded one level.

Measured on DeepSeek as a sixth condition — same 187 tasks, same pinned
database, same prompt, one more tool attached. **Answer correct and correctly
cited**:

| Task type | n | 3gpp-mcp | + `search_openapi` | paired |
|---|---|---|---|---|
| SBI request body | 50 | 94% | **100%** | 3/0, p=0.248 |
| SBI object property | 50 | **98%** | 86% | 0/6, p=0.041 |
| SBI allOf composition | 44 | 98% | **100%** | 1/0, p=1 |
| SBI oneOf alternatives | 43 | 98% | **100%** | 1/0, p=1 |
| all four | 187 | 96.8% | 96.3% | 5 won / 6 lost |

Nothing moved. The one cell that reaches p<0.05 moved down, and five of those
six answers are still correct and lost only the citation — the model writing
`MonitoringEvent API — FailureCause schema` where the gold is the bare document
name — with three of the six never calling the new tool at all. Four types
tested without correction, so one cell at p=0.041 is what noise looks like.

The score had nowhere to go. Request bodies are the type where a real gain was
available — answering *which schema an operation's body uses* is a walk through
`paths → post → requestBody → content → schema → $ref` that text similarity
cannot do, and the BM25 baseline collapses to 26% on it — and navigation was
already at 94%.

What did change is the path taken. Calls per task, over the whole track:

| Tool | 3gpp-mcp | + `search_openapi` |
|---|---|---|
| `get_openapi` | 2.18 | 1.75 |
| `list_openapi` | 0.96 | 0.66 |
| `search_openapi` | — | 0.94 |
| `search` | 0.28 | 0.07 |
| `get_section` | 0.29 | 0.13 |
| all tools | 3.81 | 3.60 |

74% of tasks call it, and every other tool goes down: it **displaces navigation
rather than adding to it**, and the total falls from 3.81 calls to 3.60. The
model that used to list the API documents and open one to find where a schema
lives now asks for it by name. That is the tool working as designed; it is not
something a benchmark whose navigation condition already answers 97% can score.

## Takeaways

- **Ask for retrieval and you get the whole effect.** Sonnet skipped searching
  on 40% of TeleQnA questions and Luna on 60%, and a question where nothing was
  retrieved measures the model, not the server. One sentence in the prompt takes
  Luna's skip rate to zero and its gain from +5.9 to +12.0pt; it moves DeepSeek,
  which already searched on 99.8%, by +0.7pt (p=0.305). With retrieval forced,
  the tools are worth +12.0pt on Luna and +12.7pt on DeepSeek.
- **Reaching the index by tool call is worth what reaching it through a
  pipeline is worth** — the two land within three points of each other on
  1,509 questions, as they should: same database, same sections, same BM25.
  A fixed-k pipeline is one query of what 3gpp-mcp can do.
- **The gain over one query comes from opening a section.** Where the model
  stopped at the search result nothing separates it from fixed-k; the only two
  significant gains are in the stratum where it followed the reference further —
  and those are the questions it could not answer unaided. On the
  specification-grounded tasks a
  quarter to a third of all calls are `get_openapi`; a BM25 index built over
  those same documents reaches them too, and still answers-and-cites 6-95%
  where 3gpp-mcp reaches 93-100%.
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
- **`search_openapi` replaces navigation without changing the answer.** On the
  SBI tasks it is called on 74% of them and cuts every other tool's usage, but
  the score is 96.8% against 96.3% — the questions were already being answered
  by opening the documents. Its value is in the calls it saves a client that
  knows a data type and not its document, which these tasks do not price.

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

- The search variant ran on the two ends — the model that skipped 60% of
  questions and the one that skipped none. Sonnet sits between them at 40%, and
  was not run.
- One prompt format. TeleQnA is multiple choice; the specification-grounded
  tasks are short-answer with a required citation. Neither is a working
  engineer's question.
- n=50 per task type, one pass per condition on that set — enough for the
  +24 to +88pt differences, not for the small ones. The `search_openapi`
  comparison is entirely made of small ones, and is reported as a null result
  rather than as four separate cells.
- What retrieval *costs* is not measured anywhere here, only what it scores. A
  tool that reaches an answer in one call instead of two looks identical in
  every table above.
- The specification-grounded tasks are generated from the same database the
  tools read. They test whether the model can find and cite what is there, not
  whether the corpus is right.
- Absolute numbers are not comparable to Telco-RAG / TelcoAI / GSMA leaderboard
  figures (different models, prompt formats and subsets); the paired same-model
  delta is the measurement.

## Why the per-question data is not here

Only the aggregate is published. A results file carries the question text, the
options and the gold answer of every item it scored, and the generated tasks
are the benchmark itself — publishing either puts it into the next crawl and
into the next model's training data, after which no number measured on it means
anything. TeleQnA is distributed as a password-protected archive for exactly
that reason, and the tasks generated from the specifications are withheld for
the same one.

What that costs: these numbers cannot be audited item by item from outside. What
is published instead is the protocol, the pinned database identifier, and the
harness that regenerates every figure from a dataset you obtain yourself.

Harness, full methodology and reproduction scripts:
[higebu/teleqna-eval](https://github.com/higebu/teleqna-eval).
