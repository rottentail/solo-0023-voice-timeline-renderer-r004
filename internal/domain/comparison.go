package domain

// ComparisonStatus is the authoritative machine-readable outcome of a render
// comparison. It is deliberately distinct from transport error kinds: a
// not-comparable pair is a successful read that carries an explicit reason.
type ComparisonStatus string

const (
	// StatusIdentical means both outputs share a format and contain exactly
	// the same PCM samples.
	StatusIdentical ComparisonStatus = "identical"
	// StatusDifferent means both outputs share a format but at least one
	// sample (or the sample length) differs.
	StatusDifferent ComparisonStatus = "different"
	// StatusNotComparable means the pair cannot be compared at the sample
	// level. The report always carries a stable reason code.
	StatusNotComparable ComparisonStatus = "not_comparable"
)

// Stable reason codes used when a comparison resolves to not_comparable.
const (
	// ReasonRenderNotCompleted: the job exists but never reached completion.
	ReasonRenderNotCompleted = "render_not_completed"
	// ReasonRenderFailed: the job reached the failed terminal state and by
	// construction has no published output.
	ReasonRenderFailed = "render_failed"
	// ReasonArtifactsMissing: the job is completed but its output folder is
	// no longer present, for example after external cleanup.
	ReasonArtifactsMissing = "render_artifacts_missing"
	// ReasonOutputUnreadable: a published output cannot be decoded.
	ReasonOutputUnreadable = "render_output_unreadable"
	// ReasonIntegrityError: a published output no longer matches the digest
	// recorded in its receipt.
	ReasonIntegrityError = "render_output_integrity_error"
	// ReasonFormatMismatch: the outputs use different PCM layouts and can
	// therefore not be compared sample by sample. The exact facts are listed
	// in RenderComparison.FormatMismatches.
	ReasonFormatMismatch = "render_format_mismatch"
)

// Stable identifiers for the kind of the first located difference.
const (
	// DifferenceSampleValue: both streams contain a frame, but the PCM value
	// at the reported channel differs.
	DifferenceSampleValue = "sample_value"
	// DifferenceLength: all shared samples match and one stream ends first.
	DifferenceLength = "length"
)

// Format fact names reported inside RenderComparison.FormatMismatches.
const (
	FormatFactSampleRate    = "sample_rate"
	FormatFactChannels      = "channels"
	FormatFactBitsPerSample = "bits_per_sample"
)

// RenderSideFacts collects the identity, state, format, and version facts for
// one side of a comparison. Output fields are omitted when the side has no
// published output.
type RenderSideFacts struct {
	RenderID      string      `json:"render_id"`
	TimelineID    string      `json:"timeline_id,omitempty"`
	State         RenderState `json:"state"`
	SchemaVersion int         `json:"schema_version,omitempty"`
	SampleRate    int         `json:"sample_rate,omitempty"`
	Channels      int         `json:"channels,omitempty"`
	BitsPerSample int         `json:"bits_per_sample,omitempty"`
	FrameCount    int64       `json:"frame_count,omitempty"`
	ByteCount     int64       `json:"byte_count,omitempty"`
	Digest        string      `json:"digest,omitempty"`
}

// FrameDifference locates the first divergence between two same-format
// outputs. Pointer values are nil on the side that has no sample at the
// position, which happens for length differences.
type FrameDifference struct {
	Kind        string `json:"kind"`
	FrameIndex  int64  `json:"frame_index"`
	Channel     int    `json:"channel"`
	SampleIndex int64  `json:"sample_index"`
	LeftValue   *int16 `json:"left_value,omitempty"`
	RightValue  *int16 `json:"right_value,omitempty"`
}

// RenderComparison is the immutable, deterministic result of comparing two
// render jobs. It never embeds timestamps so repeated comparisons of the same
// stored artifacts produce byte-identical reports.
type RenderComparison struct {
	Status           ComparisonStatus `json:"status"`
	Comparable       bool             `json:"comparable"`
	AudioIdentical   bool             `json:"audio_identical"`
	ReasonCode       string           `json:"reason_code,omitempty"`
	ReasonMessage    string           `json:"reason_message,omitempty"`
	Left             RenderSideFacts  `json:"left"`
	Right            RenderSideFacts  `json:"right"`
	FormatMismatches []string         `json:"format_mismatches,omitempty"`
	// VersionMatch reports whether both receipts use the same schema version.
	// It is omitted until both receipts are available, so an absent value
	// means "unknown", not "mismatched".
	VersionMatch    bool             `json:"version_match,omitempty"`
	ComparedFrames  int64            `json:"compared_frames,omitempty"`
	FirstDifference *FrameDifference `json:"first_difference,omitempty"`
}

// MarkNotComparable records an explicit blocking reason. Existing side facts
// and any previously gathered facts are preserved.
func (c *RenderComparison) MarkNotComparable(code, message string) {
	c.Status = StatusNotComparable
	c.Comparable = false
	c.AudioIdentical = false
	c.ReasonCode = code
	c.ReasonMessage = message
}
