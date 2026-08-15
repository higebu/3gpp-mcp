# Benchmark

How much does 3gpp-mcp improve LLM accuracy on 3GPP questions, and how much of
that a plain search baseline — or the same agent stopped one round in — already
reaches. Measured with
[3gpp-mcp-bench](https://github.com/higebu/3gpp-mcp-bench) on two task sets:
[TeleQnA](https://github.com/netop-team/TeleQnA), and tasks generated from the
specification documents themselves.

Every condition sends the model **the identical prompt, byte for byte**; only
the retrieval differs:

| Condition | What the model gets |
|---|---|
| no tools | the question, nothing else |
| BM25 fixed-k | one full-text search over the same database, top-5 sections prepended, no tools |
| one round | the same tools and prompt, the loop stopped after the first round |
| 3gpp-mcp | every MCP tool bridged in, up to 20 tool rounds, the model drives its own search |

The one-round condition exists to measure retrieval depth directly rather than
infer it, and ran only on the later task types where depth is the question.

Two more conditions exist for the 5G SBI tasks. Their answers live in OpenAPI
documents, which the server those runs used stored without putting them in its
FTS5 index — a choice in 3gpp-mcp at the time, not a fact about search — so the
clause-text fixed-k
baseline cannot reach them, and its score on those tasks says nothing about
what a retrieval pipeline can do. The two conditions exist to remove that
artifact: **BM25 over OpenAPI** queries an index built for exactly these
documents, and **oracle lookup** is handed the chunk the question names. An
oracle is not a system anyone can build — it is the ceiling a retriever could
reach if its query were always perfect. On the SBI tasks those two, not
fixed-k, are what 3gpp-mcp is measured against.

## TeleQnA

All 1,509 `Standards specifications` questions tagged `3GPP`, three repeats of
each condition, on a database pinned as `latest-v2-2026-08-11` (4,129
specifications), served by 3gpp-mcp `09330a0`:

| Model | No tools | With 3gpp-mcp | Δ | 95% CI | Win/loss¹ | McNemar | Calls/q |
|---|---|---|---|---|---|---|---|
| DeepSeek V4 Flash | 74.4% (SD 1.3) | **86.3%** (SD 0.6) | **+12.0pt** | [+10.7, +13.2] | 732 / 191 | χ²=315.9, p<10⁻⁶⁹ | 7.7 |
| Claude Sonnet 5 | 75.1% (SD 0.6) | **84.1%** (SD 0.5) | +8.9pt | [+7.7, +10.1] | 592 / 188 | χ²=208.2, p<10⁻⁴⁶ | 2.1 |
| GPT 5.6 Luna | 73.4% (SD 0.2) | **80.0%** (SD 1.0) | +6.5pt | [+5.4, +7.7] | 511 / 215 | χ²=119.9, p<10⁻²⁷ | 1.3 |

¹ questions only the tools run answered correctly / only the baseline answered
correctly. The tests pool the three repeats into 4,527 pairs; repeats of one
question are correlated, so the χ² values and CI widths are optimistic. Scored
one repeat at a time, the weakest of the nine per-repeat McNemar tests is still
p<10⁻⁷.

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

**Part of the SBI citation column above was free.** Each of those four types
opens by naming its schema *and* the API document that holds it, so the clause
to cite is written in the question. Three later types ask about one shared pool
of 21 schemas and differ only in what they give away, and their no-tools
baselines price that: told the name and the document, a model with no tools at
all cites correctly on **57%** of tasks, by copying the question back; told only
the name, **0%**; given only the words the document itself uses to say what the
schema is for, **14%**. Withholding the document is also what costs the model
the answer — at one round, 86% against 48%, 9 tasks lost to 1 gained, p=0.027 —
while withholding the *name* on top of that costs a further 5 points and nothing
that survives a test (3/4, p=1). The four types are not easy because they name
their schemas. They are easy because they say where to look.

### Measuring the depth directly, instead of inferring it

The TeleQnA split above reads the depth story out of subsets the model chose for
itself. The one-round condition measures it: the same tools, the same prompt,
the loop stopped after the first round. It is not the fixed-k baseline in
another form — the model writes its own query, picks its own tool, and may call
several — but it cannot act on what those calls returned.

Two task types were built to need more than one hop. NGAP (`TS 38.413`) and
S1AP (`TS 36.413`) give each information element its own clause and put the
ASN.1 for the entire protocol in **one** clause of 270 kB and 92 kB, against the
16 kB a tool result is truncated to, so retrieving the right clause is not the
same as holding the answer. *answer correct / answer correct **and** correctly
cited*, 50 tasks each, DeepSeek V4 Flash:

| Task type | no tools | BM25 fixed-k | one round | 3gpp-mcp (20 rounds) |
|---|---|---|---|---|
| NGAP/S1AP mandatory IEs | 14% / 0% | 32% / 22% | 14% / 14% | **100% / 100%** |
| NGAP/S1AP ASN.1 constraint | 56% / 4% | 60% / 20% | 72% / 50% | **100% / 98%** |

Against **itself, one round earlier**, the agent is **+86pt** and **+28pt**
(43/0, p<10⁻⁹; 14/0, p=0.0005). Every task in both tool conditions called at
least one tool, so none of this is diluted by a model that declined to search.
The mandatory-IE type shows the mechanism in its purest form: after one round
the model cites the right clause on **49 of 50** tasks and answers **7**. It has
found the table and cannot read it. Which clause to open is what the search
result tells you, so opening it is always the *second* round — `get_section` is
called 0.01 times per task at one round and 1.29 times at twenty.

One round also scores *below* fixed-k on that type, 14% against 32%: a search
hit is a snippet, and the pipeline's one query hands over whole clauses. One
round of an agent is not one query of a pipeline, and on this evidence it is
worth less.

**Depth is not free value, and the same condition finds where it is worth
nothing.** A third type asks which specifications a whole clause tree depends
on — 50 kB to 537 kB of text per task, a median of 57 clauses — which
`get_references` aggregates in a single call:

| | no tools | BM25 fixed-k | one round | 3gpp-mcp (20 rounds) |
|---|---|---|---|---|
| Clause-tree references | 2% | 0% | 82% | **86%** |

+84pt over the strongest baseline — 42 wins to 0 losses against no tools, and
+86pt with 43/0 against fixed-k, which here is the *weaker* of the two — and
rounds 2 through 20 are worth **+4pt, 9 wins to 7 losses, p=0.803**: nothing,
bought with 13 more tool calls per task and 30 times the prompt tokens. Where
one call already *is* the aggregation, the reading the extra rounds buy is
reading the model did not need.

### get_asn1: the measured walks, collapsed into lookups

The two depth results above are diagnoses, and `get_asn1` is the tool built
from them: given a type, IE or constant name it returns that assignment's
text with its defining clause, resolving the name across every specification
in the database when no `spec_id` is given. Re-running the ASN.1 task types
against a server with it — same pinned database, same tasks, DeepSeek V4
Flash on the official API, *answer / answer+citation* — moves exactly the
rows the diagnoses said it should:

| Task type, one round | without get_asn1 | with get_asn1 |
|---|---|---|
| NGAP/S1AP ASN.1 constraint | 72% / 50% | **98–100% / 96–98%** |
| RRC ASN.1 structure | 52% / 50% | **94% / 88%** |

Both one-round rows now sit on their twenty-round answer scores (which
stay at 94–100% with the tool attached). The two hops it removes are the
ones the corpus forced: into a 92–270 kB protocol ASN.1 clause, and — for
the RRC type, whose questions name a type but not its document — from a
bare name to the specification that defines it; of that type's 24 one-round
misses, 21 were the model aiming the lookup at the wrong specification, and
the cross-spec mode resolves all 21. Per-run tables and the failure
taxonomy are in the results repository's report.

**`search_openapi` is a cost, not an accuracy.** Repeating the three
designation types with the FTS index over the OpenAPI store dropped — the server
as it was before that index existed — changes nothing measurable: 100% answer on
all three types, 95-100% once the citation has to check out, including where the
question gives neither a name nor a document. Twenty rounds is enough to route
around a missing index. What the index buys is the work: 2.2× the tool calls and
2.8× the prompt tokens on the described type, and 4× the calls when the question
names a schema without saying where it lives.

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
- **Stopping the same agent after one round says the same thing, without an
  observational split.** On NGAP/S1AP tasks, where the answer sits in a clause
  larger than a tool result, rounds 2 through 20 are worth **+86pt** — after the
  first round the model cites the right clause on 49 of 50 tasks and answers 7.
  On clause-tree references, where one `get_references` call already spans the
  answer, the same nineteen rounds are worth **+4pt (p=0.803)** for 30× the
  prompt tokens. Depth is worth what the document's shape makes it worth.
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
  model fetched the document itself. The reverse also holds, and inflates the
  other end: a question that names both the schema and the document it lives in
  is cited correctly 57% of the time by a model with **no tools at all**, which
  is copying the question back.
- **`search_openapi` buys work, not accuracy.** Dropping the OpenAPI index
  entirely leaves the 20-round scores where they were; it costs 2.2-4× the tool
  calls and 2.8× the prompt tokens to get there.
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
- Same corpus throughout. The TeleQnA and first-nine task-type runs were served
  by 3gpp-mcp `09330a0` (11 tools); the depth and designation tracks by
  `51b1f0e`, which adds `search_openapi` as a twelfth.
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
  +24 to +88pt differences, not for the small ones. The three designation types
  are n=21: they share one pool of schemas, 22 of the corpus's 5,664 described
  schemas survive a test that no *other* schema fits the same description, and
  21 of those 22 are selected under a cap of two per API document.
- The depth and designation tracks ran on DeepSeek only. Their 20-round columns
  sit at 95-100%, where a second model can only tie; the column with room is one
  round, and that is what they were priced to measure.
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
[higebu/3gpp-mcp-bench](https://github.com/higebu/3gpp-mcp-bench).
