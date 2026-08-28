package review

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Contract is the exact engineering-contract content a review is checked
// against, plus the version label persisted alongside it.
type Contract struct {
	Version string
	Content string
}

// ContractSource identifies where BuildContext's contract is loaded
// from: a required global contract file, plus an optional appendix
// (e.g. a Foundry-specific addendum) layered on top of it.
type ContractSource struct {
	Version      string // persisted version label; never derived from file content
	GlobalPath   string // required
	AppendixPath string // optional; blank or all-whitespace appendix is treated as absent
}

// contractAppendixSeparator is inserted between the global contract and
// a present appendix, so the combined content never depends on how the
// two files happen to be terminated.
const contractAppendixSeparator = "\n\n---\n\n## Appendix\n\n"

// LoadContract reads ContractSource's global contract file and, if
// present, its appendix file, and returns the exact concatenated bytes
// as a Contract. It fails if Version or GlobalPath is blank, if
// GlobalPath cannot be read or is empty, or if AppendixPath is set but
// cannot be read.
func LoadContract(src ContractSource) (Contract, error) {
	version := strings.TrimSpace(src.Version)
	if version == "" {
		return Contract{}, fmt.Errorf("load contract: version is required")
	}
	if strings.TrimSpace(src.GlobalPath) == "" {
		return Contract{}, fmt.Errorf("load contract: global contract path is required")
	}

	global, err := os.ReadFile(src.GlobalPath)
	if err != nil {
		return Contract{}, fmt.Errorf("load contract: read global contract %s: %w", src.GlobalPath, err)
	}
	if strings.TrimSpace(string(global)) == "" {
		return Contract{}, fmt.Errorf("load contract: global contract %s is empty", src.GlobalPath)
	}

	content := string(global)
	if appendixPath := strings.TrimSpace(src.AppendixPath); appendixPath != "" {
		appendix, err := os.ReadFile(appendixPath)
		if err != nil {
			return Contract{}, fmt.Errorf("load contract: read appendix %s: %w", appendixPath, err)
		}
		if strings.TrimSpace(string(appendix)) != "" {
			content += contractAppendixSeparator + string(appendix)
		}
	}

	return Contract{Version: version, Content: content}, nil
}

// Context is the deterministic, two-pass Steward review prompt built
// from exactly one Snapshot and one Contract.
type Context struct {
	Snapshot Snapshot
	Contract Contract
	Prompt   string
}

// Report field names the prompt requires Steward's final message to use.
const (
	ReportFieldVerdict     = "verdict"
	ReportFieldPass1       = "pass1"
	ReportFieldPass2       = "pass2"
	ReportFieldEvidence    = "evidence"
	ReportFieldUncertainty = "uncertainties"
	ReportFieldUnavailable = "unavailable_repositories"
)

// BuildContext assembles the bounded, two-pass review prompt for one
// plan snapshot and one contract, plus an informational session
// attribution summary. attribution is rendered as its own prompt
// section but is deliberately never folded into snapshot itself: it
// reflects session attribution activity, not plan content, so it must
// never affect Snapshot.SHA256 or make an otherwise-still-valid review
// look stale. BuildContext fails if snapshot has no encoded JSON, or if
// contract's version or content is blank.
func BuildContext(snapshot Snapshot, contract Contract, attribution SessionAttributionSummary) (Context, error) {
	if len(snapshot.JSON) == 0 {
		return Context{}, fmt.Errorf("build context: snapshot is required")
	}
	if strings.TrimSpace(contract.Version) == "" {
		return Context{}, fmt.Errorf("build context: contract version is required")
	}
	if strings.TrimSpace(contract.Content) == "" {
		return Context{}, fmt.Errorf("build context: contract content is required")
	}

	return Context{
		Snapshot: snapshot,
		Contract: contract,
		Prompt:   buildPrompt(snapshot, contract, attribution),
	}, nil
}

// buildPrompt renders the contract, the fingerprinted plan snapshot, the
// informational session attribution summary (never part of the
// snapshot or its fingerprint), the read-only mount manifest (container
// paths and unavailable disclosures only, never a host path), and the
// fixed two-pass, strict-report instructions, in that order.
func buildPrompt(snapshot Snapshot, contract Contract, attribution SessionAttributionSummary) string {
	var b strings.Builder

	b.WriteString("# Steward plan review\n\n")
	fmt.Fprintf(&b, "Contract version: %s (sha256:%s)\n\n", contract.Version, sha256Hex([]byte(contract.Content)))
	b.WriteString("## Engineering contract\n\n")
	b.WriteString(strings.TrimSpace(contract.Content))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Plan snapshot (fingerprint sha256:%s)\n\n", snapshot.SHA256)
	b.WriteString("```json\n")
	b.Write(snapshot.JSON)
	b.WriteString("\n```\n\n")

	b.WriteString(sessionAttributionSection(attribution))
	b.WriteString(mountManifestSection(snapshot.Plan.Repositories))
	b.WriteString(passInstructions)

	return b.String()
}

// sessionAttributionSection renders attribution as an explicitly
// informational section: how many agent sessions are currently linked
// to the plan under review, and their breakdown by attribution method.
// This section is never part of the reviewed content or its fingerprint
// -- session attribution activity is not a change to the plan itself,
// so it must never make an existing, otherwise-still-valid review look
// stale. See SessionAttributionSummary in snapshot.go.
func sessionAttributionSection(attribution SessionAttributionSummary) string {
	var b strings.Builder
	b.WriteString("## Session attribution (informational; not part of the reviewed content or its fingerprint)\n\n")
	if attribution.Total == 0 {
		b.WriteString("No agent sessions are currently linked to this plan.\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%d agent session(s) are currently linked to this plan, by attribution method:\n\n", attribution.Total)
	methods := make([]string, 0, len(attribution.ByMethod))
	for method := range attribution.ByMethod {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		fmt.Fprintf(&b, "- %s: %d\n", method, attribution.ByMethod[method])
	}
	b.WriteString("\n")
	return b.String()
}

// mountManifestSection lists every plan repository's read-only mount
// point exactly as Steward will see it, never a host path.
func mountManifestSection(repos []RepositorySnapshot) string {
	var b strings.Builder
	b.WriteString("## Repository mounts (read-only)\n\n")
	if len(repos) == 0 {
		b.WriteString("(no repositories)\n\n")
		return b.String()
	}
	for _, r := range repos {
		role := "secondary"
		if r.Primary {
			role = "primary"
		}
		if r.Available {
			fmt.Fprintf(&b, "- %s (%s, position %s): mounted read-only at %s\n",
				r.Name, role, strconv.Itoa(r.Position), r.ContainerPath)
		} else {
			fmt.Fprintf(&b, "- %s (%s, position %s): UNAVAILABLE - %s\n",
				r.Name, role, strconv.Itoa(r.Position), r.UnavailableReason)
		}
	}
	b.WriteString("\nOnly the paths listed above exist for this review. Do not assume, request, or")
	b.WriteString(" invent any other filesystem location, including any host path.\n\n")
	return b.String()
}

// passInstructions is the fixed two-pass, strict-report instruction
// block appended to every review prompt.
const passInstructions = `## Procedure

You are Steward, reviewing exactly one plan snapshot against exactly one
engineering contract, in one session, in two passes. Do not skip a pass,
merge the two passes together, or read repository content before Pass 1
is complete.

### Pass 1 - plan completeness (plan-only, no repository reads)

Using only the engineering contract and the plan snapshot above, assess
whether the plan is conceptually complete: recoverable understanding,
delegated authority, concept delta, approval triggers, evidence
semantics, reviewability, and verification, as defined by the contract.
List concrete structural findings and, separately, the explicit
questions Pass 2 must resolve by reading source. Do not read any
repository file during this pass.

### Pass 2 - targeted grounding (bounded repository reads)

Only after Pass 1's questions are written, resolve them by reading
source from the read-only mounts listed above. Bound every read to the
named paths the plan or Pass 1's questions actually call out, plus at
most one adjacent hop (a direct caller, dependency, lifecycle, or test of
a named path). Do not explore beyond that bound. For every repository
marked UNAVAILABLE above, note it as unavailable rather than guessing at
its content. Recording "unverified" is a valid, honest outcome; do not
fabricate evidence to avoid it.

### Final report (strict JSON, in your final message only)

Respond with your final message containing exactly one JSON object with
these keys and no others:

- "verdict": one of "pass", "revise", "escalate".
- "pass1": Pass 1's structural findings and questions, kept separate from Pass 2.
- "pass2": Pass 2's grounded findings, kept separate from Pass 1.
- "evidence": the specific paths and lines actually inspected in Pass 2.
- "uncertainties": anything left unverified or ambiguous, including any
  question from Pass 1 that Pass 2 could not resolve.
- "unavailable_repositories": the names of any repository marked
  UNAVAILABLE above that the plan depends on.

A report missing any of these keys, adding others, or asserting a
verdict other than the three listed is malformed. Prose outside that one
JSON object in your final message is never treated as part of the report.
`
