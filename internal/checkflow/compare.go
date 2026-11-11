package checkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"voice-timeline-renderer/internal/domain"
)

// runRenderCompare exercises the read-only render diff surface across every
// supported outcome: identical outputs, a located sample difference, a
// blocking format mismatch, and explicit not-comparable states for unready,
// failed, and externally cleaned-up renders. It also proves repeated
// comparisons are stable and that comparing never changes a job or output.
func runRenderCompare(ctx context.Context, harness *Harness) error {
	first, err := sineWAV(48000, 1, 400, 220, 0.25)
	if err != nil {
		return err
	}
	second, err := sineWAV(44100, 2, 350, 330, 0.2)
	if err != nil {
		return err
	}
	if _, status, err := harness.createSource(ctx, "source_diff_a", "opening take", first); err != nil || status != 201 {
		return fmt.Errorf("opening source setup failed: status=%d err=%v", status, err)
	}
	if _, status, err := harness.createSource(ctx, "source_diff_b", "closing take", second); err != nil || status != 201 {
		return fmt.Errorf("closing source setup failed: status=%d err=%v", status, err)
	}

	// Two renders of the same deterministic timeline must be identical.
	sameSpans := []domain.TimelineSpan{
		{SourceID: "source_diff_a", SourceStartFrame: 0, SourceEndFrame: 19200, GainDB: -2, CrossfadeFrames: 0},
		{SourceID: "source_diff_b", SourceStartFrame: 0, SourceEndFrame: 15435, GainDB: 0, CrossfadeFrames: 4800},
	}
	plan, status, err := harness.createTimeline(ctx, "timeline_diff_same", "shared assembly", 48000, 2, -1, sameSpans)
	if err != nil || status != 201 {
		return fmt.Errorf("shared timeline setup failed: status=%d err=%v", status, err)
	}
	left, status, err := harness.createRender(ctx, "render_diff_left", plan.ID)
	if err != nil || status != 201 || left.State != domain.RenderCompleted {
		return fmt.Errorf("left render setup failed: status=%d state=%s err=%v", status, left.State, err)
	}
	right, status, err := harness.createRender(ctx, "render_diff_right", plan.ID)
	if err != nil || status != 201 || right.State != domain.RenderCompleted {
		return fmt.Errorf("right render setup failed: status=%d state=%s err=%v", status, right.State, err)
	}

	if err := expectIdentical(ctx, harness, left.ID, right.ID); err != nil {
		return err
	}

	// A render built with a different gain plan differs at the sample level.
	differentSpans := []domain.TimelineSpan{
		{SourceID: "source_diff_a", SourceStartFrame: 0, SourceEndFrame: 19200, GainDB: 6, CrossfadeFrames: 0},
		{SourceID: "source_diff_b", SourceStartFrame: 0, SourceEndFrame: 15435, GainDB: 0, CrossfadeFrames: 4800},
	}
	otherPlan, status, err := harness.createTimeline(ctx, "timeline_diff_other", "louder assembly", 48000, 2, -1, differentSpans)
	if err != nil || status != 201 {
		return fmt.Errorf("different timeline setup failed: status=%d err=%v", status, err)
	}
	different, status, err := harness.createRender(ctx, "render_diff_changed", otherPlan.ID)
	if err != nil || status != 201 || different.State != domain.RenderCompleted {
		return fmt.Errorf("changed render setup failed: status=%d state=%s err=%v", status, different.State, err)
	}
	if err := expectSampleDifference(ctx, harness, left.ID, different.ID); err != nil {
		return err
	}

	// A mono render has a different channel layout: this is a format fact,
	// not an audio difference.
	monoSpans := []domain.TimelineSpan{
		{SourceID: "source_diff_a", SourceStartFrame: 0, SourceEndFrame: 19200, GainDB: -2, CrossfadeFrames: 0},
	}
	monoPlan, status, err := harness.createTimeline(ctx, "timeline_diff_mono", "mono assembly", 48000, 1, -1, monoSpans)
	if err != nil || status != 201 {
		return fmt.Errorf("mono timeline setup failed: status=%d err=%v", status, err)
	}
	mono, status, err := harness.createRender(ctx, "render_diff_mono", monoPlan.ID)
	if err != nil || status != 201 || mono.State != domain.RenderCompleted {
		return fmt.Errorf("mono render setup failed: status=%d state=%s err=%v", status, mono.State, err)
	}
	if err := expectFormatMismatch(ctx, harness, left.ID, mono.ID); err != nil {
		return err
	}

	// Same channel layout but a different sample rate is also a blocking
	// format fact rather than an audio difference.
	rateSpans := []domain.TimelineSpan{
		{SourceID: "source_diff_a", SourceStartFrame: 0, SourceEndFrame: 19200, GainDB: -2, CrossfadeFrames: 0},
	}
	ratePlan, status, err := harness.createTimeline(ctx, "timeline_diff_rate", "rate assembly", 44100, 2, -1, rateSpans)
	if err != nil || status != 201 {
		return fmt.Errorf("sample-rate timeline setup failed: status=%d err=%v", status, err)
	}
	rate, status, err := harness.createRender(ctx, "render_diff_rate", ratePlan.ID)
	if err != nil || status != 201 || rate.State != domain.RenderCompleted {
		return fmt.Errorf("sample-rate render setup failed: status=%d state=%s err=%v", status, rate.State, err)
	}
	rateReport, status, err := harness.compareRenders(ctx, left.ID, rate.ID)
	if err != nil || status != 200 {
		return fmt.Errorf("sample-rate compare failed: status=%d err=%v", status, err)
	}
	if rateReport.Status != domain.StatusNotComparable || rateReport.ReasonCode != domain.ReasonFormatMismatch {
		return fmt.Errorf("sample-rate difference must be a format mismatch, got %s", summarize(rateReport))
	}
	if len(rateReport.FormatMismatches) != 1 || rateReport.FormatMismatches[0] != domain.FormatFactSampleRate {
		return fmt.Errorf("expected only a sample_rate mismatch, got %v", rateReport.FormatMismatches)
	}

	// Same format, same prefix, but one render is shorter: the first
	// difference is reported as a length boundary, not a sample value. The
	// shorter timeline keeps the identical first span and crossfade and only
	// trims the tail of the final span, so every shared sample must match.
	shortSpans := []domain.TimelineSpan{
		{SourceID: "source_diff_a", SourceStartFrame: 0, SourceEndFrame: 19200, GainDB: -2, CrossfadeFrames: 0},
		{SourceID: "source_diff_b", SourceStartFrame: 0, SourceEndFrame: 12000, GainDB: 0, CrossfadeFrames: 4800},
	}
	shortPlan, status, err := harness.createTimeline(ctx, "timeline_diff_short", "short assembly", 48000, 2, -1, shortSpans)
	if err != nil || status != 201 {
		return fmt.Errorf("short timeline setup failed: status=%d err=%v", status, err)
	}
	short, status, err := harness.createRender(ctx, "render_diff_short", shortPlan.ID)
	if err != nil || status != 201 || short.State != domain.RenderCompleted {
		return fmt.Errorf("short render setup failed: status=%d state=%s err=%v", status, short.State, err)
	}
	if err := expectLengthDifference(ctx, harness, left.ID, short.ID); err != nil {
		return err
	}

	// Unready and failed jobs are explicitly not comparable.
	now := time.Now().UTC()
	if err := harness.seedJob(domain.RenderJob{
		ID:         "render_diff_unready",
		TimelineID: plan.ID,
		State:      domain.RenderRendering,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return err
	}
	if err := harness.seedJob(domain.RenderJob{
		ID:         "render_diff_failed",
		TimelineID: plan.ID,
		State:      domain.RenderFailed,
		Error: &domain.ErrorDetail{
			Code:    "render_failed",
			Message: "simulated failure",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}
	if err := expectBlocked(ctx, harness, left.ID, "render_diff_unready", domain.ReasonRenderNotCompleted); err != nil {
		return err
	}
	if err := expectBlocked(ctx, harness, left.ID, "render_diff_failed", domain.ReasonRenderFailed); err != nil {
		return err
	}

	// A completed job whose artifacts were cleaned from disk is not
	// comparable either, and the job record itself remains completed.
	if err := harness.removeRenderArtifacts(right.ID); err != nil {
		return err
	}
	cleaned, status, err := harness.getRender(ctx, right.ID)
	if err != nil || status != 200 || cleaned.State != domain.RenderCompleted {
		return fmt.Errorf("cleaned render job changed unexpectedly: status=%d state=%s err=%v", status, cleaned.State, err)
	}
	if err := expectBlocked(ctx, harness, left.ID, right.ID, domain.ReasonArtifactsMissing); err != nil {
		return err
	}

	// Unknown identifiers remain hard 404s rather than not_comparable.
	if _, notFoundStatus, err := harness.compareRenders(ctx, left.ID, "render_that_never_existed"); err != nil {
		return err
	} else if notFoundStatus != 404 {
		return fmt.Errorf("missing render returned status=%d, want 404", notFoundStatus)
	}

	return expectReadonlyAndStable(ctx, harness, left.ID, different.ID)
}

func expectIdentical(ctx context.Context, harness *Harness, leftID, rightID string) error {
	report, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("identical compare returned status=%d", status)
	}
	if report.Status != domain.StatusIdentical || !report.Comparable || !report.AudioIdentical {
		return fmt.Errorf("expected identical report, got %s", summarize(report))
	}
	if report.FirstDifference != nil || report.FormatMismatches != nil || report.ReasonCode != "" {
		return fmt.Errorf("identical report carries difference or reason fields: %s", summarize(report))
	}
	if !report.VersionMatch {
		return fmt.Errorf("same-schema receipts must report version_match=true")
	}
	if report.Left.SampleRate != 48000 || report.Left.Channels != 2 || report.Right.Channels != 2 {
		return fmt.Errorf("side facts omit format details: %s", summarize(report))
	}
	return nil
}

func expectSampleDifference(ctx context.Context, harness *Harness, leftID, rightID string) error {
	report, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("sample compare returned status=%d", status)
	}
	if report.Status != domain.StatusDifferent || !report.Comparable || report.AudioIdentical {
		return fmt.Errorf("expected a different report, got %s", summarize(report))
	}
	if report.FirstDifference == nil {
		return fmt.Errorf("different report is missing first_difference")
	}
	difference := report.FirstDifference
	if difference.Kind != domain.DifferenceSampleValue ||
		difference.SampleIndex != difference.FrameIndex*int64(report.Left.Channels)+int64(difference.Channel) {
		return fmt.Errorf("first difference position is inconsistent: %+v", difference)
	}
	if difference.LeftValue == nil || difference.RightValue == nil || *difference.LeftValue == *difference.RightValue {
		return fmt.Errorf("first difference must carry two unequal PCM values: %+v", difference)
	}
	if report.ComparedFrames < 1 {
		return fmt.Errorf("compared_frames must be positive for same-format renders")
	}
	return nil
}

func expectFormatMismatch(ctx context.Context, harness *Harness, leftID, rightID string) error {
	report, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("format compare returned status=%d", status)
	}
	if report.Status != domain.StatusNotComparable || report.Comparable || report.AudioIdentical {
		return fmt.Errorf("expected not_comparable format report, got %s", summarize(report))
	}
	if report.ReasonCode != domain.ReasonFormatMismatch {
		return fmt.Errorf("format mismatch reason=%q, want %q", report.ReasonCode, domain.ReasonFormatMismatch)
	}
	if len(report.FormatMismatches) != 1 || report.FormatMismatches[0] != domain.FormatFactChannels {
		return fmt.Errorf("expected only a channels mismatch, got %v", report.FormatMismatches)
	}
	if report.FirstDifference != nil {
		return fmt.Errorf("format mismatch must not report a sample difference")
	}
	if report.Left.Channels == report.Right.Channels {
		return fmt.Errorf("side facts must expose the differing channel counts")
	}
	return nil
}

func expectLengthDifference(ctx context.Context, harness *Harness, leftID, rightID string) error {
	report, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("length compare returned status=%d", status)
	}
	if report.Status != domain.StatusDifferent || !report.Comparable || report.AudioIdentical {
		return fmt.Errorf("expected a length-difference report, got %s", summarize(report))
	}
	if report.FirstDifference == nil || report.FirstDifference.Kind != domain.DifferenceLength {
		return fmt.Errorf("expected a length first difference, got %+v", report.FirstDifference)
	}
	if report.FirstDifference.FrameIndex != report.ComparedFrames {
		return fmt.Errorf("length boundary must sit at the compared frame count")
	}
	if report.FirstDifference.LeftValue != nil || report.FirstDifference.RightValue != nil {
		return fmt.Errorf("length differences must not carry PCM values at the boundary")
	}
	if report.Left.FrameCount == report.Right.FrameCount {
		return fmt.Errorf("length difference requires distinct side frame counts")
	}
	return nil
}

func expectBlocked(ctx context.Context, harness *Harness, leftID, rightID, wantCode string) error {
	report, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("blocked compare returned status=%d", status)
	}
	if report.Status != domain.StatusNotComparable || report.Comparable {
		return fmt.Errorf("expected not_comparable report, got %s", summarize(report))
	}
	if report.ReasonCode != wantCode || report.ReasonMessage == "" {
		return fmt.Errorf("blocked reason=%q message=%q, want %q", report.ReasonCode, report.ReasonMessage, wantCode)
	}
	return nil
}

func expectReadonlyAndStable(ctx context.Context, harness *Harness, leftID, rightID string) error {
	beforeJob, status, err := harness.getRender(ctx, leftID)
	if err != nil || status != 200 {
		return fmt.Errorf("cannot snapshot job before repeat compare: status=%d err=%v", status, err)
	}
	beforeOutput, status, err := harness.getOutput(ctx, leftID)
	if err != nil || status != 200 {
		return fmt.Errorf("cannot snapshot output before repeat compare: status=%d err=%v", status, err)
	}

	first, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil || status != 200 {
		return fmt.Errorf("repeat compare failed: status=%d err=%v", status, err)
	}
	second, status, err := harness.compareRenders(ctx, rightID, leftID)
	if err != nil || status != 200 {
		return fmt.Errorf("reversed compare failed: status=%d err=%v", status, err)
	}
	if first.Status != domain.StatusDifferent || second.Status != domain.StatusDifferent {
		return fmt.Errorf("repeat comparisons lost their status: %s / %s", first.Status, second.Status)
	}
	// Direction swaps the side facts and the left/right values but must keep
	// the same located frame, channel, kind, and compared frame count.
	if first.FirstDifference == nil || second.FirstDifference == nil ||
		first.FirstDifference.FrameIndex != second.FirstDifference.FrameIndex ||
		first.FirstDifference.Channel != second.FirstDifference.Channel ||
		first.FirstDifference.Kind != second.FirstDifference.Kind ||
		first.ComparedFrames != second.ComparedFrames {
		return fmt.Errorf("reversed comparison disagrees on the first difference")
	}
	if *first.FirstDifference.LeftValue != *second.FirstDifference.RightValue ||
		*first.FirstDifference.RightValue != *second.FirstDifference.LeftValue {
		return fmt.Errorf("reversed comparison must swap the two PCM values")
	}
	// A third identical call must produce byte-identical JSON, proving the
	// report carries no nondeterministic fields such as timestamps.
	third, status, err := harness.compareRenders(ctx, leftID, rightID)
	if err != nil || status != 200 {
		return fmt.Errorf("third compare failed: status=%d err=%v", status, err)
	}
	firstJSON, _ := json.Marshal(first)
	thirdJSON, _ := json.Marshal(third)
	if string(firstJSON) != string(thirdJSON) {
		return fmt.Errorf("repeated comparison is not stable:\n%s\n%s", firstJSON, thirdJSON)
	}

	afterJob, status, err := harness.getRender(ctx, leftID)
	if err != nil || status != 200 {
		return fmt.Errorf("cannot read job after compare: status=%d err=%v", status, err)
	}
	afterOutput, status, err := harness.getOutput(ctx, leftID)
	if err != nil || status != 200 {
		return fmt.Errorf("cannot read output after compare: status=%d err=%v", status, err)
	}
	if afterJob.UpdatedAt != beforeJob.UpdatedAt || afterJob.State != beforeJob.State {
		return fmt.Errorf("comparison modified the render job record")
	}
	if len(afterOutput) != len(beforeOutput) {
		return fmt.Errorf("comparison modified the render output bytes")
	}
	for index := range beforeOutput {
		if beforeOutput[index] != afterOutput[index] {
			return fmt.Errorf("comparison modified the render output bytes")
		}
	}
	return nil
}

func summarize(report domain.RenderComparison) string {
	encoded, _ := json.Marshal(report)
	return string(encoded)
}
