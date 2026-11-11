package render

import (
	"voice-timeline-renderer/internal/audiocodec"
	"voice-timeline-renderer/internal/domain"
)

// ComparisonInput is one side of a render comparison. The service is
// responsible for ensuring the render is completed and that its artifacts are
// readable and intact before building this input; the comparison itself is a
// pure, side-effect-free operation over the supplied bytes and receipts.
type ComparisonInput struct {
	WAV     []byte
	Receipt domain.RenderReceipt
}

// CompareOutputs compares two completed render outputs. It never mutates its
// inputs, performs no I/O, and never embeds wall-clock data, so repeated
// comparisons of the same artifacts always yield an identical report.
func CompareOutputs(left, right ComparisonInput) domain.RenderComparison {
	report := domain.RenderComparison{
		Left:  sideFacts(left.Receipt),
		Right: sideFacts(right.Receipt),
	}

	leftPCM, leftInfo, leftErr := audiocodec.DecodePCM16(left.WAV)
	rightPCM, rightInfo, rightErr := audiocodec.DecodePCM16(right.WAV)
	if leftErr != nil || rightErr != nil {
		report.MarkNotComparable(
			domain.ReasonOutputUnreadable,
			"a published render output cannot be decoded as 16-bit PCM WAV",
		)
		return report
	}

	report.VersionMatch = left.Receipt.SchemaVersion == right.Receipt.SchemaVersion

	if mismatches := formatMismatches(leftInfo, rightInfo); len(mismatches) > 0 {
		report.FormatMismatches = mismatches
		report.MarkNotComparable(
			domain.ReasonFormatMismatch,
			"render outputs use different PCM formats and cannot be compared sample by sample",
		)
		return report
	}

	report.Comparable = true
	sharedFrames := leftInfo.FrameCount
	if rightInfo.FrameCount < sharedFrames {
		sharedFrames = rightInfo.FrameCount
	}
	report.ComparedFrames = sharedFrames
	channels := leftInfo.Channels

	for frame := int64(0); frame < sharedFrames; frame++ {
		for channel := 0; channel < channels; channel++ {
			sampleIndex := frame*int64(channels) + int64(channel)
			leftValue := leftPCM[sampleIndex]
			rightValue := rightPCM[sampleIndex]
			if leftValue != rightValue {
				leftCopy := leftValue
				rightCopy := rightValue
				report.Status = domain.StatusDifferent
				report.FirstDifference = &domain.FrameDifference{
					Kind:        domain.DifferenceSampleValue,
					FrameIndex:  frame,
					Channel:     channel,
					SampleIndex: sampleIndex,
					LeftValue:   &leftCopy,
					RightValue:  &rightCopy,
				}
				return report
			}
		}
	}

	if leftInfo.FrameCount != rightInfo.FrameCount {
		shorter := leftInfo.FrameCount
		if rightInfo.FrameCount < shorter {
			shorter = rightInfo.FrameCount
		}
		report.Status = domain.StatusDifferent
		report.FirstDifference = &domain.FrameDifference{
			Kind:        domain.DifferenceLength,
			FrameIndex:  shorter,
			Channel:     0,
			SampleIndex: shorter * int64(channels),
		}
		return report
	}

	report.Status = domain.StatusIdentical
	report.AudioIdentical = true
	return report
}

func sideFacts(receipt domain.RenderReceipt) domain.RenderSideFacts {
	return domain.RenderSideFacts{
		RenderID:      receipt.RenderID,
		TimelineID:    receipt.TimelineID,
		State:         domain.RenderCompleted,
		SchemaVersion: receipt.SchemaVersion,
		SampleRate:    receipt.Output.SampleRate,
		Channels:      receipt.Output.Channels,
		BitsPerSample: receipt.Output.BitsPerSample,
		FrameCount:    receipt.Output.FrameCount,
		ByteCount:     receipt.Output.ByteCount,
		Digest:        receipt.Output.Digest,
	}
}

func formatMismatches(left, right audiocodec.WAVInfo) []string {
	mismatches := make([]string, 0, 3)
	if left.SampleRate != right.SampleRate {
		mismatches = append(mismatches, domain.FormatFactSampleRate)
	}
	if left.Channels != right.Channels {
		mismatches = append(mismatches, domain.FormatFactChannels)
	}
	if left.BitsPerSample != right.BitsPerSample {
		mismatches = append(mismatches, domain.FormatFactBitsPerSample)
	}
	return mismatches
}
