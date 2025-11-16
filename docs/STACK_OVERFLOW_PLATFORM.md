# Stack Overflow Platform Plan

## Objectives
- Add Stack Overflow as a supported bounty platform for question and answer content.
- Enable both ingestion (content verification) and outbound publishing hooks consistent with existing platforms.
- Maintain alignment with Temporal workflows, LLM prompts, and Forohtoo wallet orchestration needs.

## Required Capabilities
- **Content Types**: Questions (`ContentKindQuestion`) and Answers (`ContentKindAnswer`).
- **Data Source**: Stack Exchange REST API (`/questions/{ids}`, `/answers/{ids}`) with optional filter strings for expanded fields.
- **Auth**: Support API key + access token (if rate limits require); fallback to keyless mode for low-volume polling.
- **Rate Limits**: Honor Stack Exchange's request quotas and backoff headers.

## Implementation Steps
1. **Domain Updates**
   - Add `PlatformStackOverflow` and new content kinds in `abb/activity.go`.
   - Extend validation in `http/handlers_bounty.go` so bounty creation accepts `{platform: stackoverflow, content_kind: question|answer}`.
   - Update LLM prompt scaffolding (`handleCreateBounty` schema + prompt templates) to describe the new options.
2. **Activity Layer**
   - Create `abb/activity_stackoverflow.go` with:
     - Dependency struct for API key, site (`stackoverflow`), throttle window.
     - Fetch helpers `fetchStackOverflowQuestion` and `fetchStackOverflowAnswer` returning normalized structs (title, body Markdown, owner/user id, score, tags, accepted status, link).
     - Timeout- and retry-aware HTTP client honoring `backoff` headers.
   - Wire new cases into `PullContentActivity` to marshal fetched payloads.
   - Add targeted unit tests with golden fixtures for both endpoints (use recorded JSON under `testdata/stackoverflow/`).
3. **Assessment & LLM**
   - Ensure `AssessContent` logic can interpret returned JSON (e.g., check for accepted answers, minimum score).
   - Adjust LLM moderation/inference prompts to mention Stack Overflow vocabulary and sample requirements.
4. **Configuration**
   - Introduce env vars `STACKOVERFLOW_API_KEY`, `STACKOVERFLOW_ACCESS_TOKEN`, `STACKOVERFLOW_SITE`.
   - Document defaults in `.env.*.example` and `docs/new-providers.md`.
5. **UI & API Surfacing**
   - Update API response structs (if necessary) so frontend consumers can display Stack Overflow metadata (link, tags, author reputation).
   - Extend any bounty templates or CLI flows that enumerate supported platforms.

## Testing & Observability
- Add unit tests for parsing, API error handling, and rate limit retries.
- Extend workflow integration tests to mock Stack Overflow responses.
- Capture metrics (latency, success count) via existing logging hooks; add alerting thresholds for 403/502 spikes.

## Risks & Mitigations
- **Rate Limit Exhaustion**: Cache question/answer payloads to avoid redundant calls; respect `backoff` hints.
- **Content Edits After Approval**: Store revision ID and re-validate on payout to ensure content remained compliant.
- **Markdown Rendering**: Normalize HTML vs Markdown fields before LLM assessment to avoid hallucinated formatting.
