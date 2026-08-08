package charliequalification

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (d *LiveDriver) leaderKillFailover(ctx context.Context, request ScenarioRequest) (result ScenarioResult) {
	const scenario = "leader_kill_failover"
	fixture := d.fixtures.LeaderKillFailover
	if d.leaderFailover == nil || d.approverToken == "" || !validCandidate(request.Candidate) || !validActionFixture(fixture) || !d.fixtureIDsAreIsolated() {
		return Unsupported(scenario)
	}
	original, before, ok := d.beginAuthorityProof(ctx, "auto")
	if !ok {
		return Unsupported(scenario)
	}
	baselineDigest, digestErr := leaderRestorationDigest(original, request.Candidate)
	if digestErr != nil {
		_ = d.restoreMode(original)
		return Unsupported(scenario)
	}

	localSessionID := ""
	expectedToolDelta := uint64(0)
	var stream *failoverEventStream
	defer func() {
		if stream != nil {
			stream.Close()
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanupFailed := d.leaderFailover.WaitReady(cleanupCtx, int(original.Data.Agent.DesiredReplicas)) != nil
		if localSessionID != "" && d.abortFixtureSession(cleanupCtx, localSessionID, fixture.Stimulus.AbortRequestID) != nil {
			cleanupFailed = true
		}
		if d.restoreMode(original) != nil {
			cleanupFailed = true
		}
		final, statusErr := d.status(cleanupCtx)
		finalDigest, finalDigestErr := leaderRestorationDigest(final, request.Candidate)
		if statusErr != nil || finalDigestErr != nil || finalDigest != baselineDigest || !d.observeProductCallDelta(cleanupCtx, before, expectedToolDelta, 0) {
			cleanupFailed = true
		}
		if cleanupFailed {
			markCleanupFailed(&result, scenario)
		}
	}()

	leaderBefore, ordinal, mapped, mapErr := d.waitForMappedLeader(ctx, request.Candidate, "auto")
	if mapErr != nil || !mapped {
		return Unsupported(scenario)
	}
	podUID, replicas, err := d.leaderFailover.Snapshot(ctx, ordinal)
	if err != nil || replicas != int(original.Data.Agent.DesiredReplicas) {
		return Unsupported(scenario)
	}
	localSessionID, err = d.createFixtureSession(ctx, fixture.Stimulus)
	if err != nil {
		return Unsupported(scenario)
	}
	stream, err = d.openFailoverEventStream(ctx, localSessionID)
	if err != nil || stream.AwaitConnected(ctx) != nil {
		return Unsupported(scenario)
	}
	// Re-read the application-level leader after the potentially slow stream
	// setup. The exact instance, epoch, and ordinal must still match the pod UID
	// captured above immediately before deletion.
	rebound, reboundOrdinal, reboundMapped, reboundErr := d.waitForMappedLeader(ctx, request.Candidate, "auto")
	if reboundErr != nil || !reboundMapped || reboundOrdinal != ordinal || rebound.Data.Agent.LeaderReplica != leaderBefore.Data.Agent.LeaderReplica || rebound.Data.Agent.FencingEpoch != leaderBefore.Data.Agent.FencingEpoch {
		return Unsupported(scenario)
	}

	killStarted := time.Now().UTC()
	failoverCtx, failoverCancel := context.WithTimeout(ctx, 2*time.Minute)
	podReadyAt, err := d.leaderFailover.DeleteAndWaitReplacement(failoverCtx, ordinal, podUID)
	if err != nil {
		failoverCancel()
		return Unsupported(scenario)
	}
	leaderAfter, err := d.waitForReplacementLeader(failoverCtx, request.Candidate, leaderBefore)
	failoverCancel()
	if err != nil {
		return Unsupported(scenario)
	}
	replacementReadyAt := time.Now().UTC()
	if podReadyAt.After(replacementReadyAt) {
		replacementReadyAt = podReadyAt
	}

	receipt, err := d.sendStimulus(ctx, localSessionID, fixture.Stimulus)
	if err != nil {
		return Unsupported(scenario)
	}
	// Once accepted, the cleanup path requires exactly one call even if the
	// client loses the response. This prevents an ambiguous dispatch from being
	// mistaken for a harmless failed qualification.
	expectedToolDelta = 1
	actionID, firstEventID, err := stream.AwaitAction(ctx, receipt.TurnID, fixture.Capability)
	if err != nil || !d.waitForOperation(ctx, actionID, fixture.Capability, before, 1) {
		return Unsupported(scenario)
	}

	observation := LeaderFailoverObservation{
		Candidate: candidateObservation(request.Candidate), BaselineStateDigest: baselineDigest,
		LeaderBefore: leaderBefore.Data.Agent.LeaderReplica, KilledInstance: leaderBefore.Data.Agent.LeaderReplica,
		LeaderAfter: leaderAfter.Data.Agent.LeaderReplica, EpochBefore: uint64(leaderBefore.Data.Agent.FencingEpoch), EpochAfter: uint64(leaderAfter.Data.Agent.FencingEpoch),
		KillStartedAt: killStarted, ReplacementReadyAt: replacementReadyAt,
		SSEConnectionID: localSessionID, SSELastEventBefore: "stream-connected", SSEFirstEventAfter: firstEventID, SSEResumed: true,
		ActionID: actionID, ActionExecutionCount: 1, ActionCompletionCount: 1,
	}
	return validateLeaderFailover(request.Candidate, observation)
}

func candidateObservation(candidate Candidate) CandidateObservation {
	return CandidateObservation{Commit: candidate.Commit, Version: candidate.Version, CentralImageDigest: candidate.CentralImageDigest, AgentImageDigest: candidate.AgentImageDigest, CentralChartDigest: candidate.CentralChartDigest, AgentChartDigest: candidate.AgentChartDigest}
}

func (d *LiveDriver) waitForMappedLeader(ctx context.Context, candidate Candidate, authority string) (statusEnvelope, int, bool, error) {
	deadline := time.Now().Add(d.proofTimeout)
	for {
		status, err := d.status(ctx)
		if err == nil && leaderStatusMatchesCandidate(status, candidate, authority) {
			for _, replica := range status.Data.Agent.Replicas {
				if replica.InstanceID == status.Data.Agent.LeaderReplica && replica.Role == "leader" && replica.State == "ready" && replica.Ordinal >= 0 && replica.Ordinal < int(status.Data.Agent.DesiredReplicas) {
					return status, replica.Ordinal, true, nil
				}
			}
		}
		if err != nil || !time.Now().Before(deadline) || waitContext(ctx, d.proofPoll) != nil {
			return statusEnvelope{}, 0, false, errors.New("leader identity could not be bound to a ready replica")
		}
	}
}

func (d *LiveDriver) waitForReplacementLeader(ctx context.Context, candidate Candidate, prior statusEnvelope) (statusEnvelope, error) {
	deadline := time.Now().Add(d.proofTimeout)
	for {
		status, err := d.status(ctx)
		if err == nil && leaderStatusMatchesCandidate(status, candidate, "auto") &&
			status.Data.Agent.LeaderReplica != prior.Data.Agent.LeaderReplica && status.Data.Agent.FencingEpoch > prior.Data.Agent.FencingEpoch {
			return status, nil
		}
		if err != nil || !time.Now().Before(deadline) || waitContext(ctx, d.proofPoll) != nil {
			return statusEnvelope{}, errors.New("replacement leader was not elected within the qualification bound")
		}
	}
}

func leaderStatusMatchesCandidate(status statusEnvelope, candidate Candidate, authority string) bool {
	return status.Data.Connection.Connected && status.Data.Connection.CentralVersion == candidate.Version &&
		status.Data.Agent.DesiredReplicas >= 2 && status.Data.Agent.DesiredReplicas <= 20 && status.Data.Agent.ReadyReplicas == status.Data.Agent.DesiredReplicas &&
		validEvidenceID(status.Data.Agent.LeaderReplica) && status.Data.Agent.FencingEpoch > 0 &&
		status.Data.Agent.AgentVersion == candidate.Version && status.Data.Agent.ImageDigest == candidate.AgentImageDigest && status.Data.Agent.ChartDigest == candidate.AgentChartDigest &&
		status.Data.Mode.Requested == authority && status.Data.Mode.Authoritative == authority && !status.Data.Mode.EmergencyDisabled
}

func leaderRestorationDigest(status statusEnvelope, candidate Candidate) (string, error) {
	if !leaderStatusMatchesCandidate(status, candidate, "read_only") || !status.Data.Connection.DisclosureAcknowledged || status.Data.Connection.DisclosureDigest == "" || status.Data.Connection.DisclosureDigest != status.Data.Mode.DisclosureDigest {
		return "", errors.New("leader failover restoration state is incomplete")
	}
	value := struct {
		CentralVersion   string `json:"central_version"`
		DisclosureDigest string `json:"disclosure_digest"`
		DesiredReplicas  int32  `json:"desired_replicas"`
		ReadyReplicas    int32  `json:"ready_replicas"`
		AgentVersion     string `json:"agent_version"`
		ImageDigest      string `json:"image_digest"`
		ChartDigest      string `json:"chart_digest"`
		Authority        string `json:"authority"`
	}{status.Data.Connection.CentralVersion, status.Data.Connection.DisclosureDigest, status.Data.Agent.DesiredReplicas, status.Data.Agent.ReadyReplicas, status.Data.Agent.AgentVersion, status.Data.Agent.ImageDigest, status.Data.Agent.ChartDigest, status.Data.Mode.Authoritative}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type failoverEventStream struct {
	cancel  context.CancelFunc
	body    io.ReadCloser
	scanner *bufio.Scanner
}

func (d *LiveDriver) openFailoverEventStream(ctx context.Context, localSessionID string) (*failoverEventStream, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	endpoint := d.base.ResolveReference(&url.URL{Path: "/api/v1/charlie/sessions/" + url.PathEscape(localSessionID) + "/events/"})
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		cancel()
		return nil, errors.New("failover stream request is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+d.approverToken)
	request.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Transport: d.client.Transport, CheckRedirect: d.client.CheckRedirect}
	response, err := client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		cancel()
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, errors.New("failover stream did not open")
	}
	limited := &io.LimitedReader{R: response.Body, N: (1 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	return &failoverEventStream{cancel: cancel, body: response.Body, scanner: scanner}, nil
}

func (s *failoverEventStream) AwaitConnected(ctx context.Context) error {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == ": connected" {
			return nil
		}
		if line != "" && !strings.HasPrefix(line, ":") {
			return errors.New("failover stream emitted data before its connection boundary")
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("failover stream closed before connecting")
}

func (s *failoverEventStream) AwaitAction(ctx context.Context, turnID, capability string) (string, string, error) {
	eventID, eventName := "", ""
	dataLines := make([]string, 0, 2)
	firstTurnEventID := ""
	actions := map[string]struct{}{}
	matched := false
	frames := 0
	process := func() (bool, string, error) {
		if len(dataLines) == 0 {
			eventID, eventName = "", ""
			return false, "", nil
		}
		frames++
		if frames > maxQualificationStreamEvents || !validEvidenceID(eventID) {
			return false, "", errors.New("failover stream evidence exceeded its bound")
		}
		var event streamedActionEvent
		if json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &event) != nil || (eventName != "" && event.Type != "" && eventName != event.Type) {
			return false, "", errors.New("failover stream frame is invalid")
		}
		dataLines = dataLines[:0]
		if event.TurnID == turnID && firstTurnEventID == "" {
			firstTurnEventID = eventID
		}
		if event.ActionID != "" {
			if event.TurnID != turnID || !validEvidenceID(event.ActionID) {
				return false, "", errors.New("failover stream action binding is invalid")
			}
			actions[event.ActionID] = struct{}{}
			if len(actions) != 1 {
				return false, "", errors.New("failover stream returned more than one action")
			}
			eventCapability := strings.TrimSpace(event.Data.Data.Capability)
			if eventCapability != "" && eventCapability != capability {
				return false, "", errors.New("failover stream capability changed")
			}
			matched = matched || eventCapability == capability
		}
		terminal := event.Type
		if terminal == "" {
			terminal = eventName
		}
		eventID, eventName = "", ""
		if event.TurnID == turnID && (terminal == "turn.failed" || terminal == "turn.aborted" || terminal == "charlie.error") {
			return false, "", errors.New("failover turn failed")
		}
		if event.TurnID == turnID && terminal == "turn.completed" {
			if len(actions) != 1 || !matched || firstTurnEventID == "" {
				return false, "", errors.New("failover action completion is incomplete")
			}
			for actionID := range actions {
				return true, actionID, nil
			}
		}
		return false, "", nil
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		switch {
		case line == "":
			done, actionID, err := process()
			if err != nil {
				return "", "", err
			}
			if done {
				return actionID, firstTurnEventID, nil
			}
		case strings.HasPrefix(line, "id:"):
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, ":"):
		default:
			return "", "", errors.New("failover stream line is invalid")
		}
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	return "", "", errors.New("failover stream ended before action completion")
}

func (s *failoverEventStream) Close() {
	s.cancel()
	_ = s.body.Close()
}
