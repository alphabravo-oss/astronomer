package charliequalification

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// IsolationState is deliberately closed. The qualification hook cannot turn
// its observer into a general-purpose cluster or network inspection surface.
type IsolationState string

const (
	IsolationColdFeatureDisabled     IsolationState = "cold_feature_disabled"
	IsolationColdConnectionDisabled  IsolationState = "cold_connection_disabled"
	IsolationOperationalWireDisabled IsolationState = "operational_wire_disabled"
)

type IsolationInventory struct {
	Processes uint64
	Listeners uint64
	Timers    uint64
}

type DirectionalPacketCounts struct {
	Ingress uint64
	Egress  uint64
}

type IsolationPacketCounts struct {
	DNS DirectionalPacketCounts
	TCP DirectionalPacketCounts
	UDP DirectionalPacketCounts
}

type IsolationRuntimeCounters struct {
	HealthStatusReads        uint64
	LifecycleControlRequests uint64
	HeartbeatAttempts        uint64
	HeartbeatSuccesses       uint64
	WorkClaims               uint64
	SessionsStarted          uint64
	ModelRequests            uint64
	CapabilityCalls          uint64
}

type IsolationDownstreamCounter struct {
	ConnectionAttempts uint64
	Requests           uint64
	Responses          uint64
}

type IsolationDownstreamCounters struct {
	CentralControl IsolationDownstreamCounter
	CentralWork    IsolationDownstreamCounter
	ProductMCP     IsolationDownstreamCounter
}

// IsolationControlCounters contains only Charlie's fixed protocol classes.
// It cannot carry a URL, peer, payload, signature, tool name, or arbitrary
// label. Counts are summed across ingress and egress for qualification.
type IsolationControlCounters struct {
	VerifiedSignedHeartbeat  uint64
	VerifiedSignedEnable     uint64
	VerifiedSignedModeSync   uint64
	VerifiedSignedDisconnect uint64
	VerifiedSignedOther      uint64
	RejectedAuth             uint64
	NonControl               uint64
}

type IsolationObservation struct {
	State      IsolationState
	Duration   time.Duration
	Inventory  IsolationInventory
	Packets    IsolationPacketCounts
	Runtime    IsolationRuntimeCounters
	Downstream IsolationDownstreamCounters
	Control    IsolationControlCounters
}

// IsolationObserver prepares only a fixed typed state before its mutation so a
// cold observation can retain a bounded network target. The prepared observer
// then measures after the state is applied. Implementations return
// measurements, never a caller-selected command or a precomputed verdict.
type IsolationObserver interface {
	Prepare(context.Context, IsolationState) (PreparedIsolationObserver, error)
}

type PreparedIsolationObserver interface {
	Observe(context.Context, time.Duration) (IsolationObservation, error)
}

func coldIsolationProved(observation IsolationObservation, state IsolationState) bool {
	return observation.State == state && observation.Duration > 0 &&
		observation.Inventory.Processes == 0 && observation.Inventory.Listeners == 0 && observation.Inventory.Timers == 0 &&
		observation.Packets.DNS.Ingress == 0 && observation.Packets.DNS.Egress == 0 &&
		observation.Packets.TCP.Ingress == 0 && observation.Packets.TCP.Egress == 0 &&
		observation.Packets.UDP.Ingress == 0 && observation.Packets.UDP.Egress == 0
}

func controlProtocolOnly(observation IsolationObservation) bool {
	if observation.State != IsolationOperationalWireDisabled || observation.Duration <= 0 {
		return false
	}
	runtime := observation.Runtime
	if runtime.LifecycleControlRequests != 0 || runtime.WorkClaims != 0 || runtime.SessionsStarted != 0 || runtime.ModelRequests != 0 || runtime.CapabilityCalls != 0 {
		return false
	}
	for _, counter := range []IsolationDownstreamCounter{observation.Downstream.CentralWork, observation.Downstream.ProductMCP} {
		if counter.ConnectionAttempts != 0 || counter.Requests != 0 || counter.Responses != 0 {
			return false
		}
	}
	control := observation.Control
	if control.VerifiedSignedEnable != 0 || control.VerifiedSignedModeSync != 0 || control.VerifiedSignedDisconnect != 0 || control.VerifiedSignedOther != 0 || control.RejectedAuth != 0 || control.NonControl != 0 {
		return false
	}
	central := observation.Downstream.CentralControl
	return central.ConnectionAttempts == central.Requests && central.Responses <= central.Requests &&
		runtime.HeartbeatAttempts == central.Requests && runtime.HeartbeatSuccesses == central.Responses &&
		control.VerifiedSignedHeartbeat == central.Requests
}

type IsolationObserverConfig struct {
	KubectlBinary    string
	TCPDumpBinary    string
	Kubeconfig       string
	Namespace        string
	Release          string
	StatefulSet      string
	Service          string
	CaptureInterface string
	MetricSources    []MetricSource
	HTTPClient       *http.Client
	AllowHTTP        bool
	PollInterval     time.Duration
}

type isolationCommandRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type execIsolationCommandRunner struct{}

func (execIsolationCommandRunner) Output(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var output boundedBuffer
	output.maximum = 4 << 10
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || output.exceeded {
		return nil, errors.New("fixed isolation collector failed")
	}
	return output.Bytes(), nil
}

type packetCounter interface {
	Count(context.Context, time.Duration, []string) (IsolationPacketCounts, error)
}

type kubectlTCPDumpIsolationObserver struct {
	runner           isolationCommandRunner
	kubectl          string
	tcpdump          string
	kubeconfig       string
	namespace        string
	release          string
	statefulSet      string
	service          string
	captureInterface string
	metricSources    []metricEndpoint
	client           *http.Client
	poll             time.Duration
	packets          packetCounter
}

var (
	binaryPathPattern       = regexp.MustCompile(`^(?:[A-Za-z0-9._+-]+|/[A-Za-z0-9._+/-]+)$`)
	networkInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
)

func NewKubectlTCPDumpIsolationObserver(config IsolationObserverConfig) (IsolationObserver, error) {
	kubectl := strings.TrimSpace(config.KubectlBinary)
	if kubectl == "" {
		kubectl = "kubectl"
	}
	tcpdump := strings.TrimSpace(config.TCPDumpBinary)
	if tcpdump == "" {
		tcpdump = "tcpdump"
	}
	config.Kubeconfig = strings.TrimSpace(config.Kubeconfig)
	config.Namespace = strings.TrimSpace(config.Namespace)
	config.Release = strings.TrimSpace(config.Release)
	config.StatefulSet = strings.TrimSpace(config.StatefulSet)
	config.Service = strings.TrimSpace(config.Service)
	config.CaptureInterface = strings.TrimSpace(config.CaptureInterface)
	if !binaryPathPattern.MatchString(kubectl) || !binaryPathPattern.MatchString(tcpdump) || config.Kubeconfig == "" ||
		!kubernetesNamePattern.MatchString(config.Namespace) || !kubernetesNamePattern.MatchString(config.Release) || !kubernetesNamePattern.MatchString(config.StatefulSet) ||
		!kubernetesNamePattern.MatchString(config.Service) ||
		!networkInterfacePattern.MatchString(config.CaptureInterface) {
		return nil, errors.New("isolation observer configuration is unsafe")
	}
	if info, err := os.Stat(config.Kubeconfig); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("isolation observer kubeconfig must be owner-only")
	}
	poll := config.PollInterval
	if poll == 0 {
		poll = time.Second
	}
	if poll < time.Millisecond || poll > 10*time.Second {
		return nil, errors.New("isolation observer polling is outside its safe bound")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       15 * time.Second,
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	metrics := make([]metricEndpoint, 0, len(config.MetricSources))
	for _, source := range config.MetricSources {
		parsed, err := safeOperatorURL(source.URL, config.AllowHTTP)
		if err != nil {
			return nil, errors.New("isolation metrics URL is unsafe")
		}
		metrics = append(metrics, metricEndpoint{url: parsed, token: strings.TrimSpace(source.Token)})
	}
	observer := &kubectlTCPDumpIsolationObserver{
		runner: execIsolationCommandRunner{}, kubectl: kubectl, tcpdump: tcpdump, kubeconfig: config.Kubeconfig,
		namespace: config.Namespace, release: config.Release, statefulSet: config.StatefulSet, service: config.Service,
		captureInterface: config.CaptureInterface, metricSources: metrics, client: client, poll: poll,
	}
	observer.packets = &tcpdumpPacketCounter{binary: tcpdump, captureInterface: config.CaptureInterface}
	return observer, nil
}

type preparedKubectlTCPDumpIsolationObserver struct {
	parent    *kubectlTCPDumpIsolationObserver
	state     IsolationState
	targetIPs []string
}

func (o *kubectlTCPDumpIsolationObserver) Prepare(ctx context.Context, state IsolationState) (PreparedIsolationObserver, error) {
	if state != IsolationColdFeatureDisabled && state != IsolationColdConnectionDisabled && state != IsolationOperationalWireDisabled {
		return nil, errors.New("isolation state is not supported")
	}
	prepared := &preparedKubectlTCPDumpIsolationObserver{parent: o, state: state}
	if state != IsolationOperationalWireDisabled {
		workload, err := o.kubectlOutput(ctx, "get", "statefulset", o.statefulSet, "-o", `go-template={{if .metadata.uid}}1{{end}}`)
		workloadCount, countErr := parseFixedCount(workload, true)
		if err != nil || countErr != nil || workloadCount != 1 {
			return nil, errors.New("Charlie agent workload target is unavailable")
		}
		targets, err := o.targetPodIPs(ctx)
		if err != nil || len(targets) == 0 {
			return nil, errors.New("Charlie agent capture targets are unavailable")
		}
		prepared.targetIPs = targets
	}
	return prepared, nil
}

func (p *preparedKubectlTCPDumpIsolationObserver) Observe(ctx context.Context, dwell time.Duration) (IsolationObservation, error) {
	if dwell < time.Millisecond || dwell > 15*time.Minute {
		return IsolationObservation{}, errors.New("isolation observation request is outside its fixed bounds")
	}
	o := p.parent
	state := p.state
	observation := IsolationObservation{State: state, Duration: dwell}
	if state == IsolationOperationalWireDisabled {
		before, err := o.metricSnapshot(ctx)
		if err != nil {
			return IsolationObservation{}, err
		}
		if err = waitContext(ctx, dwell); err != nil {
			return IsolationObservation{}, errors.New("isolation observation interrupted")
		}
		after, err := o.metricSnapshot(ctx)
		if err != nil {
			return IsolationObservation{}, err
		}
		observation.Runtime, observation.Downstream, observation.Control, err = isolationMetricDelta(before, after)
		return observation, err
	}
	defer func() {
		for index := range p.targetIPs {
			p.targetIPs[index] = ""
		}
		p.targetIPs = nil
	}()

	packetResult := make(chan struct {
		value IsolationPacketCounts
		err   error
	}, 1)
	captureCtx, cancelCapture := context.WithCancel(ctx)
	defer cancelCapture()
	go func() {
		value, err := o.packets.Count(captureCtx, dwell, p.targetIPs)
		packetResult <- struct {
			value IsolationPacketCounts
			err   error
		}{value: value, err: err}
	}()
	deadline := time.Now().Add(dwell)
	for {
		inventory, err := o.inventory(ctx)
		if err != nil {
			cancelCapture()
			<-packetResult
			return IsolationObservation{}, err
		}
		observation.Inventory.Processes = maxUint64(observation.Inventory.Processes, inventory.Processes)
		observation.Inventory.Listeners = maxUint64(observation.Inventory.Listeners, inventory.Listeners)
		observation.Inventory.Timers = maxUint64(observation.Inventory.Timers, inventory.Timers)
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := o.poll
		if wait > remaining {
			wait = remaining
		}
		if err = waitContext(ctx, wait); err != nil {
			cancelCapture()
			<-packetResult
			return IsolationObservation{}, errors.New("isolation observation interrupted")
		}
	}
	packets := <-packetResult
	if packets.err != nil {
		return IsolationObservation{}, packets.err
	}
	observation.Packets = packets.value
	return observation, nil
}

func maxUint64(first, second uint64) uint64 {
	if first > second {
		return first
	}
	return second
}

func (o *kubectlTCPDumpIsolationObserver) kubectlOutput(ctx context.Context, args ...string) ([]byte, error) {
	base := []string{"--kubeconfig", o.kubeconfig, "--namespace", o.namespace}
	return o.runner.Output(ctx, o.kubectl, append(base, args...)...)
}

func (o *kubectlTCPDumpIsolationObserver) selector() string {
	return "app.kubernetes.io/name=charlie-agent,app.kubernetes.io/instance=" + o.release
}

func (o *kubectlTCPDumpIsolationObserver) targetPodIPs(ctx context.Context) ([]string, error) {
	output, err := o.kubectlOutput(ctx, "get", "pods", "-l", o.selector(), "-o", `go-template={{range .items}}{{if .status.podIP}}{{.status.podIP}}{{"\n"}}{{end}}{{end}}`)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 || len(fields) > 20 {
		return nil, errors.New("Charlie agent capture target count is outside the qualification bound")
	}
	result := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		ip := net.ParseIP(field)
		if ip == nil {
			return nil, errors.New("Charlie agent capture target is malformed")
		}
		normalized := ip.String()
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func (o *kubectlTCPDumpIsolationObserver) inventory(ctx context.Context) (IsolationInventory, error) {
	// These fixed templates emit decimal counts only. Resource documents,
	// addresses, names, process arguments, and endpoint content never enter the
	// observer process or its result.
	processOutput, err := o.kubectlOutput(ctx, "get", "pods", "-l", o.selector(), "-o", `go-template={{range .items}}1 {{range .status.containerStatuses}}{{if .state.running}}1 {{end}}{{end}}{{end}}`)
	if err != nil {
		return IsolationInventory{}, err
	}
	listenerOutput, err := o.kubectlOutput(ctx, "get", "endpoints", o.service, "--ignore-not-found", "-o", "go-template={{range .subsets}}{{range .addresses}}1 {{end}}{{range .notReadyAddresses}}1 {{end}}{{end}}")
	if err != nil {
		return IsolationInventory{}, err
	}
	timerOutput, err := o.kubectlOutput(ctx, "get", "cronjobs", "-l", o.selector(), "-o", `go-template={{range .items}}1 {{end}}`)
	if err != nil {
		return IsolationInventory{}, err
	}
	processes, err := parseFixedCount(processOutput, true)
	if err != nil {
		return IsolationInventory{}, err
	}
	listeners, err := parseFixedCount(listenerOutput, true)
	if err != nil {
		return IsolationInventory{}, err
	}
	timers, err := parseFixedCount(timerOutput, true)
	if err != nil {
		return IsolationInventory{}, err
	}
	// Every agent pod owns retry, heartbeat, and worker timers inside its
	// process. Count the pod sentinel as timer presence even when no separate
	// CronJob exists; this fails closed for pending and terminating remnants.
	podSentinels := uint64(0)
	if processes > 0 {
		podSentinels = 1
	}
	// A remaining pod is conservatively treated as owning at least one listener
	// and one timer even if it has already fallen out of its Service endpoints.
	// This covers unready/terminating containers and fails closed without exec,
	// process listings, socket addresses, or timer definitions.
	return IsolationInventory{Processes: processes, Listeners: listeners + podSentinels, Timers: timers + podSentinels}, nil
}

func parseFixedCount(output []byte, sumTokens bool) (uint64, error) {
	text := strings.TrimSpace(string(output))
	if text == "" {
		if sumTokens {
			return 0, nil
		}
		return 0, errors.New("fixed collector returned no count")
	}
	fields := strings.Fields(text)
	if !sumTokens && len(fields) != 1 {
		return 0, errors.New("fixed collector returned malformed count")
	}
	var total uint64
	for _, field := range fields {
		value, err := strconv.ParseUint(field, 10, 53)
		if err != nil || (sumTokens && value != 1) || total > 9_007_199_254_740_991-value {
			return 0, errors.New("fixed collector returned malformed count")
		}
		total += value
	}
	return total, nil
}

type isolationMetricSnapshot struct {
	Runtime    IsolationRuntimeCounters
	Downstream IsolationDownstreamCounters
	Control    IsolationControlCounters
}

var isolationMetricNames = func() map[string]func(*isolationMetricSnapshot, uint64) {
	result := map[string]func(*isolationMetricSnapshot, uint64){
		"charlie_agent_health_status_reads_total":        func(v *isolationMetricSnapshot, n uint64) { v.Runtime.HealthStatusReads += n },
		"charlie_agent_lifecycle_control_requests_total": func(v *isolationMetricSnapshot, n uint64) { v.Runtime.LifecycleControlRequests += n },
		"charlie_agent_heartbeat_attempts_total":         func(v *isolationMetricSnapshot, n uint64) { v.Runtime.HeartbeatAttempts += n },
		"charlie_agent_heartbeat_successes_total":        func(v *isolationMetricSnapshot, n uint64) { v.Runtime.HeartbeatSuccesses += n },
		"charlie_agent_work_claim_requests_total":        func(v *isolationMetricSnapshot, n uint64) { v.Runtime.WorkClaims += n },
		"charlie_agent_sessions_started_total":           func(v *isolationMetricSnapshot, n uint64) { v.Runtime.SessionsStarted += n },
		"charlie_agent_model_requests_total":             func(v *isolationMetricSnapshot, n uint64) { v.Runtime.ModelRequests += n },
		"charlie_agent_capability_calls_total":           func(v *isolationMetricSnapshot, n uint64) { v.Runtime.CapabilityCalls += n },
		"charlie_agent_control_rejected_auth_total":      func(v *isolationMetricSnapshot, n uint64) { v.Control.RejectedAuth += n },
	}
	for _, class := range []string{"central_control", "central_work", "product_mcp"} {
		class := class
		for _, operation := range []string{"connection_attempts", "requests", "responses"} {
			operation := operation
			result["charlie_agent_downstream_"+class+"_"+operation+"_total"] = func(v *isolationMetricSnapshot, n uint64) {
				counter := downstreamCounterByClass(&v.Downstream, class)
				switch operation {
				case "connection_attempts":
					counter.ConnectionAttempts += n
				case "requests":
					counter.Requests += n
				case "responses":
					counter.Responses += n
				}
			}
		}
	}
	controlSetters := map[string]func(*IsolationControlCounters, uint64){
		"verified_signed_heartbeat":  func(v *IsolationControlCounters, n uint64) { v.VerifiedSignedHeartbeat += n },
		"verified_signed_enable":     func(v *IsolationControlCounters, n uint64) { v.VerifiedSignedEnable += n },
		"verified_signed_mode_sync":  func(v *IsolationControlCounters, n uint64) { v.VerifiedSignedModeSync += n },
		"verified_signed_disconnect": func(v *IsolationControlCounters, n uint64) { v.VerifiedSignedDisconnect += n },
		"verified_signed_other":      func(v *IsolationControlCounters, n uint64) { v.VerifiedSignedOther += n },
		"non_control":                func(v *IsolationControlCounters, n uint64) { v.NonControl += n },
	}
	for class, setter := range controlSetters {
		setter := setter
		result["charlie_agent_control_egress_"+class+"_total"] = func(v *isolationMetricSnapshot, n uint64) { setter(&v.Control, n) }
	}
	return result
}()

func downstreamCounterByClass(value *IsolationDownstreamCounters, class string) *IsolationDownstreamCounter {
	switch class {
	case "central_control":
		return &value.CentralControl
	case "central_work":
		return &value.CentralWork
	case "product_mcp":
		return &value.ProductMCP
	default:
		return &value.CentralWork
	}
}

func (o *kubectlTCPDumpIsolationObserver) metricSnapshot(ctx context.Context) (isolationMetricSnapshot, error) {
	if len(o.metricSources) == 0 {
		return isolationMetricSnapshot{}, errors.New("isolation metrics are not configured")
	}
	result := isolationMetricSnapshot{}
	found := make(map[string]bool, len(isolationMetricNames))
	for _, endpoint := range o.metricSources {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url.String(), nil)
		request.Header.Set("Accept", "text/plain")
		if endpoint.token != "" {
			request.Header.Set("Authorization", "Bearer "+endpoint.token)
		}
		response, err := o.client.Do(request)
		if err != nil || response.StatusCode != http.StatusOK {
			if response != nil {
				_ = response.Body.Close()
			}
			return isolationMetricSnapshot{}, errors.New("isolation metrics unavailable")
		}
		scanner := bufio.NewScanner(io.LimitReader(response.Body, (16<<20)+1))
		scanner.Buffer(make([]byte, 1024), 64<<10)
		for scanner.Scan() {
			line := scanner.Bytes()
			name, value, ok := fixedMetricLine(line)
			if !ok {
				if wantedMetricCandidate(line) {
					_ = response.Body.Close()
					return isolationMetricSnapshot{}, errors.New("isolation metric has a forbidden shape")
				}
				continue
			}
			setter, wanted := isolationMetricNames[name]
			if !wanted {
				continue
			}
			setter(&result, value)
			found[name] = true
		}
		scanErr := scanner.Err()
		_ = response.Body.Close()
		if scanErr != nil {
			return isolationMetricSnapshot{}, errors.New("isolation metrics malformed")
		}
	}
	for name := range isolationMetricNames {
		if !found[name] {
			return isolationMetricSnapshot{}, fmt.Errorf("required isolation metric is absent")
		}
	}
	return result, nil
}

func wantedMetricCandidate(line []byte) bool {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] == '#' {
		return false
	}
	end := bytes.IndexAny(line, "{ \t")
	if end < 0 {
		end = len(line)
	}
	_, wanted := isolationMetricNames[string(line[:end])]
	return wanted
}

func fixedMetricLine(line []byte) (string, uint64, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] == '#' {
		return "", 0, false
	}
	fields := bytes.Fields(line)
	if len(fields) != 2 || bytes.ContainsAny(fields[0], "{}") {
		return "", 0, false
	}
	name := string(fields[0])
	if _, wanted := isolationMetricNames[name]; !wanted {
		return "", 0, false
	}
	parsed, err := strconv.ParseFloat(string(fields[1]), 64)
	if err != nil || parsed < 0 || parsed > 9_007_199_254_740_991 || parsed != float64(uint64(parsed)) {
		return "", 0, false
	}
	return name, uint64(parsed), true
}

func isolationMetricDelta(before, after isolationMetricSnapshot) (IsolationRuntimeCounters, IsolationDownstreamCounters, IsolationControlCounters, error) {
	// Reflection would make future fields easy to forget. The explicit fixed
	// order makes a newly added class a compile-time review point.
	b := []uint64{before.Runtime.HealthStatusReads, before.Runtime.LifecycleControlRequests, before.Runtime.HeartbeatAttempts, before.Runtime.HeartbeatSuccesses, before.Runtime.WorkClaims, before.Runtime.SessionsStarted, before.Runtime.ModelRequests, before.Runtime.CapabilityCalls}
	a := []uint64{after.Runtime.HealthStatusReads, after.Runtime.LifecycleControlRequests, after.Runtime.HeartbeatAttempts, after.Runtime.HeartbeatSuccesses, after.Runtime.WorkClaims, after.Runtime.SessionsStarted, after.Runtime.ModelRequests, after.Runtime.CapabilityCalls}
	delta := make([]uint64, len(a))
	for i := range a {
		if a[i] < b[i] {
			return IsolationRuntimeCounters{}, IsolationDownstreamCounters{}, IsolationControlCounters{}, errors.New("isolation metric reset during observation")
		}
		delta[i] = a[i] - b[i]
	}
	runtime := IsolationRuntimeCounters{HealthStatusReads: delta[0], LifecycleControlRequests: delta[1], HeartbeatAttempts: delta[2], HeartbeatSuccesses: delta[3], WorkClaims: delta[4], SessionsStarted: delta[5], ModelRequests: delta[6], CapabilityCalls: delta[7]}
	var downstream IsolationDownstreamCounters
	for _, pair := range []struct{ before, after, target *IsolationDownstreamCounter }{
		{&before.Downstream.CentralControl, &after.Downstream.CentralControl, &downstream.CentralControl}, {&before.Downstream.CentralWork, &after.Downstream.CentralWork, &downstream.CentralWork},
		{&before.Downstream.ProductMCP, &after.Downstream.ProductMCP, &downstream.ProductMCP},
	} {
		if pair.after.ConnectionAttempts < pair.before.ConnectionAttempts || pair.after.Requests < pair.before.Requests || pair.after.Responses < pair.before.Responses {
			return IsolationRuntimeCounters{}, IsolationDownstreamCounters{}, IsolationControlCounters{}, errors.New("isolation metric reset during observation")
		}
		*pair.target = IsolationDownstreamCounter{pair.after.ConnectionAttempts - pair.before.ConnectionAttempts, pair.after.Requests - pair.before.Requests, pair.after.Responses - pair.before.Responses}
	}
	bc := []uint64{before.Control.VerifiedSignedHeartbeat, before.Control.VerifiedSignedEnable, before.Control.VerifiedSignedModeSync, before.Control.VerifiedSignedDisconnect, before.Control.VerifiedSignedOther, before.Control.RejectedAuth, before.Control.NonControl}
	ac := []uint64{after.Control.VerifiedSignedHeartbeat, after.Control.VerifiedSignedEnable, after.Control.VerifiedSignedModeSync, after.Control.VerifiedSignedDisconnect, after.Control.VerifiedSignedOther, after.Control.RejectedAuth, after.Control.NonControl}
	cd := make([]uint64, len(ac))
	for i := range ac {
		if ac[i] < bc[i] {
			return IsolationRuntimeCounters{}, IsolationDownstreamCounters{}, IsolationControlCounters{}, errors.New("isolation metric reset during observation")
		}
		cd[i] = ac[i] - bc[i]
	}
	control := IsolationControlCounters{VerifiedSignedHeartbeat: cd[0], VerifiedSignedEnable: cd[1], VerifiedSignedModeSync: cd[2], VerifiedSignedDisconnect: cd[3], VerifiedSignedOther: cd[4], RejectedAuth: cd[5], NonControl: cd[6]}
	return runtime, downstream, control, nil
}

type tcpdumpPacketCounter struct{ binary, captureInterface string }

type tcpdumpCapture struct {
	direction, protocol string
	summary             *tcpdumpSummary
	command             *exec.Cmd
}

type tcpdumpSummary struct {
	mu             sync.Mutex
	partial        []byte
	count          uint64
	found, invalid bool
}

func (s *tcpdumpSummary) Write(value []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range value {
		if b == '\n' {
			s.consume()
			s.partial = s.partial[:0]
			continue
		}
		if len(s.partial) < 256 {
			s.partial = append(s.partial, b)
		} else {
			s.invalid = true
		}
	}
	return len(value), nil
}
func (s *tcpdumpSummary) consume() {
	fields := bytes.Fields(s.partial)
	if len(fields) == 3 && string(fields[1]) == "packets" && string(fields[2]) == "captured" {
		value, err := strconv.ParseUint(string(fields[0]), 10, 53)
		if err != nil {
			s.invalid = true
			return
		}
		s.count = value
		s.found = true
	}
}
func (s *tcpdumpSummary) result() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consume()
	if s.invalid || !s.found {
		return 0, errors.New("tcpdump did not return a content-free count")
	}
	return s.count, nil
}

func (c *tcpdumpPacketCounter) Count(ctx context.Context, dwell time.Duration, targetIPs []string) (IsolationPacketCounts, error) {
	if len(targetIPs) == 0 || len(targetIPs) > 20 {
		return IsolationPacketCounts{}, errors.New("tcpdump target scope is unavailable")
	}
	captures := make([]tcpdumpCapture, 0, 6)
	for _, direction := range []string{"in", "out"} {
		addressDirection := "dst"
		if direction == "out" {
			addressDirection = "src"
		}
		targetTerms := make([]string, 0, len(targetIPs))
		for _, target := range targetIPs {
			if net.ParseIP(target) == nil {
				return IsolationPacketCounts{}, errors.New("tcpdump target scope is malformed")
			}
			targetTerms = append(targetTerms, addressDirection+" host "+target)
		}
		targetFilter := "(" + strings.Join(targetTerms, " or ") + ")"
		for _, protocol := range []string{"(tcp or udp) and port 53", "tcp", "udp"} {
			summary := &tcpdumpSummary{}
			command := exec.Command(c.binary, "-i", c.captureInterface, "-Q", direction, "-n", "-q", "-w", "/dev/null", protocol+" and "+targetFilter)
			command.Stdout = io.Discard
			command.Stderr = summary
			if err := command.Start(); err != nil {
				stopTCPDumpCommands(captures, syscall.SIGINT)
				return IsolationPacketCounts{}, errors.New("tcpdump observation could not start")
			}
			captures = append(captures, tcpdumpCapture{direction: direction, protocol: protocol, summary: summary, command: command})
		}
	}
	if err := waitContext(ctx, dwell); err != nil {
		stopTCPDumpCommands(captures, os.Kill)
		return IsolationPacketCounts{}, errors.New("tcpdump observation interrupted")
	}
	for i := range captures {
		_ = captures[i].command.Process.Signal(syscall.SIGINT)
	}
	result := IsolationPacketCounts{}
	for i := range captures {
		waitDone := make(chan error, 1)
		go func(command *exec.Cmd) { waitDone <- command.Wait() }(captures[i].command)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			_ = captures[i].command.Process.Kill()
			<-waitDone
		}
		count, err := captures[i].summary.result()
		if err != nil {
			return IsolationPacketCounts{}, err
		}
		target := &result.DNS
		if captures[i].protocol == "tcp" {
			target = &result.TCP
		}
		if captures[i].protocol == "udp" {
			target = &result.UDP
		}
		if captures[i].direction == "in" {
			target.Ingress = count
		} else {
			target.Egress = count
		}
	}
	return result, nil
}

func stopTCPDumpCommands(captures []tcpdumpCapture, signal os.Signal) {
	for i := range captures {
		_ = captures[i].command.Process.Signal(signal)
	}
	for i := range captures {
		waitDone := make(chan error, 1)
		go func(command *exec.Cmd) { waitDone <- command.Wait() }(captures[i].command)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			_ = captures[i].command.Process.Kill()
			<-waitDone
		}
	}
}
