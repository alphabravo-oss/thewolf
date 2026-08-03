package scannerrelease

// EffectiveDefinitionCommit returns the immutable source revision that a
// candidate must build and publish. A managed proposal starts from
// DefinitionCommit and records its generated revision in ProposedCommit; once
// present, every downstream trust boundary must bind the proposed revision.
func EffectiveDefinitionCommit(candidate *Candidate) string {
	if candidate == nil {
		return ""
	}
	if candidate.ProposedCommit != "" {
		return candidate.ProposedCommit
	}
	return candidate.DefinitionCommit
}
