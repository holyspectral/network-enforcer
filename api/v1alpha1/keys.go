package v1alpha1

const (
	ProposalPromoteLabelKey    = "security.rancher.io/promote"
	PolicyPromotedFromLabelKey = "security.rancher.io/promoted-from"

	// ViolationAcknowledgePrefix is the prefix of annotation key used to acknowledge a violation.
	// An annotation of the form security.rancher.io/acknowledge-<id>: "<reason>" moves the
	// violation record with that ID into AcknowledgedViolations.
	ViolationAcknowledgePrefix = "security.rancher.io/acknowledge-"
)
