package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"voice-timeline-renderer/internal/domain"
	"voice-timeline-renderer/internal/render"
)

// CompareRenders reads two completed render jobs and compares their published
// outputs. The operation is strictly read-only: it never transitions a job,
// never writes metadata, and never creates or removes files, so jobs and
// outputs are byte-for-byte unchanged afterwards. Unfinished, failed, or
// cleaned-up renders produce an explicit not_comparable report instead of a
// transport error.
func (s *Service) CompareRenders(
	ctx context.Context,
	leftID string,
	rightID string,
) (domain.RenderComparison, error) {
	if err := ctx.Err(); err != nil {
		return domain.RenderComparison{}, err
	}
	if err := domain.ValidateResourceID("left_render_id", leftID); err != nil {
		return domain.RenderComparison{}, err
	}
	if err := domain.ValidateResourceID("right_render_id", rightID); err != nil {
		return domain.RenderComparison{}, err
	}

	leftJob, err := s.repo.GetJob(leftID)
	if err != nil {
		return domain.RenderComparison{}, err
	}
	rightJob, err := s.repo.GetJob(rightID)
	if err != nil {
		return domain.RenderComparison{}, err
	}

	report := domain.RenderComparison{
		Left:  jobSideFacts(leftJob),
		Right: jobSideFacts(rightJob),
	}

	if code, message, blocked := blockedReason(leftJob); blocked {
		report.MarkNotComparable(code, message)
		return report, nil
	}
	if code, message, blocked := blockedReason(rightJob); blocked {
		report.MarkNotComparable(code, message)
		return report, nil
	}

	leftWAV, leftReceipt, err := s.repo.GetRenderOutput(leftID)
	if err != nil {
		if code, message, handled := missingArtifactsReason(err); handled {
			report.MarkNotComparable(code, message)
			return report, nil
		}
		return domain.RenderComparison{}, err
	}
	rightWAV, rightReceipt, err := s.repo.GetRenderOutput(rightID)
	if err != nil {
		if code, message, handled := missingArtifactsReason(err); handled {
			report.MarkNotComparable(code, message)
			return report, nil
		}
		return domain.RenderComparison{}, err
	}

	if !digestMatches(leftWAV, leftReceipt.Output.Digest) {
		report.MarkNotComparable(
			domain.ReasonIntegrityError,
			"left render output bytes do not match the digest recorded in its receipt",
		)
		return report, nil
	}
	if !digestMatches(rightWAV, rightReceipt.Output.Digest) {
		report.MarkNotComparable(
			domain.ReasonIntegrityError,
			"right render output bytes do not match the digest recorded in its receipt",
		)
		return report, nil
	}

	return render.CompareOutputs(
		render.ComparisonInput{WAV: leftWAV, Receipt: leftReceipt},
		render.ComparisonInput{WAV: rightWAV, Receipt: rightReceipt},
	), nil
}

// blockedReason maps a durable job state to the reason that prevents a
// sample-level comparison. A completed job is never blocked here even if its
// artifacts were later removed; that case is resolved against storage.
func blockedReason(job domain.RenderJob) (string, string, bool) {
	switch job.State {
	case domain.RenderCompleted:
		return "", "", false
	case domain.RenderFailed:
		return domain.ReasonRenderFailed,
			"render job reached the failed terminal state and has no published output",
			true
	default:
		return domain.ReasonRenderNotCompleted,
			"render job has not completed, so its output is not available for comparison",
			true
	}
}

// missingArtifactsReason recognizes the storage signal for a completed job
// whose output folder is absent, for example after external cleanup. Any
// other storage failure is returned to the caller as a real error.
func missingArtifactsReason(err error) (string, string, bool) {
	appErr := domain.AsAppError(err)
	if appErr.Detail.Code == domain.ReasonArtifactsMissing {
		return domain.ReasonArtifactsMissing,
			"completed render job has no readable output artifacts on disk",
			true
	}
	return "", "", false
}

func digestMatches(payload []byte, expectedDigest string) bool {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]) == expectedDigest
}

// jobSideFacts builds the identity and recorded format facts available from
// the durable job record alone, before any output is read.
func jobSideFacts(job domain.RenderJob) domain.RenderSideFacts {
	facts := domain.RenderSideFacts{
		RenderID:   job.ID,
		TimelineID: job.TimelineID,
		State:      job.State,
	}
	if job.Output != nil {
		facts.SampleRate = job.Output.SampleRate
		facts.Channels = job.Output.Channels
		facts.BitsPerSample = job.Output.BitsPerSample
		facts.FrameCount = job.Output.FrameCount
		facts.ByteCount = job.Output.ByteCount
		facts.Digest = job.Output.Digest
	}
	return facts
}
