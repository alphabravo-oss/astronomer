package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/coder/websocket"
)

func pendingDecommissionFixture(t *testing.T, h *DecommissionHandler, ctx context.Context) (*protocol.Message, *decommissionReply) {
	t.Helper()
	ctx, owner := withDecommissionReply(ctx)
	t.Cleanup(owner.close)
	body, err := json.Marshal(protocol.DecommissionPayload{RemoveAgentDeployment: true})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := h.HandleDecommission(ctx, &protocol.Message{Type: protocol.MsgDecommission, Payload: body, StreamID: "owned-stream", RequestID: "owned-request"})
	if err != nil {
		t.Fatal(err)
	}
	var ack protocol.DecommissionAckPayload
	if err := json.Unmarshal(reply.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	step := stepByName(ack, "remove_agent_deployment")
	if step == nil || !step.Success || step.Removed != 1 || owner.cleanup == nil {
		t.Fatal("valid original owned target was not admitted", step)
	}
	return reply, owner
}

func TestDecommissionSelfDeleteRefusesMissingWriterOwnershipAndUnownedUID(t *testing.T) {
	for _, kind := range []string{"missing-writer", "unmanaged", "missing-uid"} {
		t.Run(kind, func(t *testing.T) {
			cs := seedFullFootprint()
			ctx := context.Background()
			if kind != "missing-writer" {
				var owner *decommissionReply
				ctx, owner = withDecommissionReply(ctx)
				defer owner.close()
			}
			if kind != "missing-writer" {
				dep, err := cs.AppsV1().Deployments(DefaultAgentNamespace).Get(ctx, DefaultAgentDeploymentName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if kind == "unmanaged" {
					dep.Labels = nil
				} else {
					dep.UID = ""
				}
				if _, err := cs.AppsV1().Deployments(DefaultAgentNamespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			h := newHandler(t, cs, nil)
			plan, step := h.prepareSelfDelete(ctx, protocol.DecommissionPayload{RemoveAgentDeployment: true})
			if plan != nil || step.Success || step.Error == "" || h.selfDeletePending.Load() || len(deleteActions(cs)) != 0 {
				t.Fatal("unowned self-delete admitted", step)
			}
		})
	}
}

func TestDecommissionQueuedOrFailedACKNeverStartsMutation(t *testing.T) {
	for _, kind := range []string{"queued-only", "failed-write", "canceled-owner"} {
		t.Run(kind, func(t *testing.T) {
			cs := seedFullFootprint()
			h := newHandler(t, cs, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reply, owner := pendingDecommissionFixture(t, h, ctx)
			tc := NewTunnelClient(testConfig(), testLogger())
			t.Cleanup(tc.discardDecommissionReplies)
			// Queue ownership uses the same private context token; enqueue is not write.
			if err := tc.sendHandlerReply(context.WithValue(ctx, decommissionReplyKey{}, owner), reply, dispatchExempt, false); err != nil {
				t.Fatal(err)
			}
			if len(deleteActions(cs)) != 0 {
				t.Fatal("enqueue started mutation before ACK write")
			}
			switch kind {
			case "queued-only":
				tc.discardDecommissionReplies()
			case "failed-write":
				reply.Payload = json.RawMessage("{")
				if tc.writeQueued(ctx, reply) {
					t.Fatal("malformed ACK write unexpectedly succeeded")
				}
			case "canceled-owner":
				cancel()
				tc.runDecommissionReply(reply)
			}
			if len(deleteActions(cs)) != 0 || h.selfDeletePending.Load() {
				t.Fatal("failed or canceled write mutated target or stranded ownership")
			}
		})
	}
}

func TestDecommissionCapturedObjectsRejectReplacementOwnershipAndDeleteRace(t *testing.T) {
	for _, kind := range []string{"deployment-recreated", "deployment-relabelled", "deployment-missing-rv", "namespace-recreated", "role-recreated", "delete-race"} {
		t.Run(kind, func(t *testing.T) {
			cs := seedFullFootprint()
			h := newHandler(t, cs, nil)
			ctx := context.Background()
			plan, err := h.captureSelfDelete(ctx, fullFootprintPayload())
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(kind, "deployment-") {
				dep, err := cs.AppsV1().Deployments(DefaultAgentNamespace).Get(ctx, DefaultAgentDeploymentName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if kind == "deployment-recreated" {
					dep.UID = "replacement-deployment"
				} else if kind == "deployment-missing-rv" {
					dep.ResourceVersion = ""
				} else {
					dep.Labels = nil
				}
				if _, err := cs.AppsV1().Deployments(DefaultAgentNamespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "namespace-recreated" {
				ns, err := cs.CoreV1().Namespaces().Get(ctx, DefaultAgentNamespace, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				ns.UID = "replacement-namespace"
				if _, err := cs.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "role-recreated" {
				role, err := cs.RbacV1().ClusterRoles().Get(ctx, "astronomer-agent", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				role.UID = "replacement-role"
				if _, err := cs.RbacV1().ClusterRoles().Update(ctx, role, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "delete-race" {
				cs.PrependReactor("delete", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
					opts := action.(k8stesting.DeleteAction).GetDeleteOptions()
					if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != plan.deployment.uid {
						t.Error("UID delete precondition missing")
					}
					dep := &appsv1.Deployment{ObjectMeta: partOfMeta(DefaultAgentDeploymentName, DefaultAgentNamespace)}
					dep.UID = "replacement-between-get-and-delete"
					if err := cs.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), dep, DefaultAgentNamespace); err != nil {
						t.Error(err)
					}
					return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, DefaultAgentDeploymentName, errors.New("UID precondition rejected"))
				})
			}
			if err := plan.execute(ctx); err == nil {
				t.Fatal("changed owned target cleanup returned success")
			}
			deletions := deleteActions(cs)
			if strings.HasPrefix(kind, "deployment-") || kind == "delete-race" {
				if contains(deletions, "namespaces/"+DefaultAgentNamespace) {
					t.Fatal("deployment refusal still cascaded namespace")
				}
				if _, err := cs.AppsV1().Deployments(DefaultAgentNamespace).Get(ctx, DefaultAgentDeploymentName, metav1.GetOptions{}); err != nil {
					t.Fatal("replacement deployment removed", err)
				}
			}
			if kind == "namespace-recreated" && contains(deletions, "namespaces/"+DefaultAgentNamespace) {
				t.Fatal("recreated namespace deleted")
			}
			if kind == "role-recreated" && contains(deletions, "clusterroles/astronomer-agent") {
				t.Fatal("recreated role deleted")
			}
		})
	}
}

func TestDecommissionActualACKWriteOwnsContextBoundDeleteAndJoinedTunnel(t *testing.T) {
	entered := make(chan struct{})
	stopped := make(chan struct{})
	var enterOnce, stopOnce sync.Once
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: DefaultAgentDeploymentName, Namespace: DefaultAgentNamespace, UID: "original-agent", ResourceVersion: "7", Labels: map[string]string{tPartOf: "astronomer"}}})
			return
		}
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var opts metav1.DeleteOptions
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			t.Error(err)
		}
		if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != "original-agent" || opts.Preconditions.ResourceVersion == nil || *opts.Preconditions.ResourceVersion != "7" {
			t.Error("actual API delete lost original UID/current RV")
		}
		enterOnce.Do(func() { close(entered) })
		<-r.Context().Done()
		stopOnce.Do(func() { close(stopped) })
	}))
	t.Cleanup(api.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := &DecommissionHandler{clientset: client, log: testLogger()}
	peerEntered := make(chan struct{})
	peerStopped := make(chan struct{})
	ackRead := make(chan struct{})
	var peerEnterOnce, peerStopOnce, ackOnce sync.Once
	body, err := json.Marshal(protocol.DecommissionPayload{RemoveAgentDeployment: true})
	if err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		peerEnterOnce.Do(func() { close(peerEntered) })
		defer peerStopOnce.Do(func() { close(peerStopped) })
		defer conn.CloseNow()
		connect, _, err := readFrame(r.Context(), conn)
		if err != nil {
			return
		}
		var request protocol.Message
		if err := json.Unmarshal(connect, &request); err != nil || request.Type != protocol.MsgConnect {
			t.Error("actual tunnel handshake missing")
			return
		}
		connectACK, _ := json.Marshal(protocol.ConnectAckPayload{Accepted: true})
		for _, msg := range []*protocol.Message{
			{Type: protocol.MsgConnectAck, Payload: connectACK, Timestamp: time.Now().UTC()},
			{Type: protocol.MsgDecommission, Payload: body, StreamID: "owned-stream", RequestID: "owned-request"},
		} {
			frame, err := json.Marshal(msg)
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, frame); err != nil {
				return
			}
		}
		for {
			frame, _, err := readFrame(r.Context(), conn)
			if err != nil {
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(frame, &msg); err != nil {
				t.Error(err)
				return
			}
			if msg.Type == protocol.MsgDecommissionAck {
				if msg.StreamID != "owned-stream" || msg.RequestID != "owned-request" {
					t.Error("ACK identity changed")
				}
				ackOnce.Do(func() { close(ackRead) })
			}
		}
	}))
	t.Cleanup(peer.Close)
	cfg := testConfig()
	cfg.ServerURL = "ws://" + strings.TrimPrefix(peer.URL, "http://")
	tc := NewTunnelClient(cfg, testLogger())
	tc.RegisterHandler(protocol.MsgDecommission, h.HandleDecommission)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if tc.conn != nil {
				_ = tc.conn.CloseNow()
			}
		case <-time.After(2 * time.Second):
			t.Error("owned tunnel did not join")
		}
		select {
		case <-entered:
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Error("actual delete request did not drain")
			}
		default:
		}
		select {
		case <-peerEntered:
			select {
			case <-peerStopped:
			case <-time.After(2 * time.Second):
				t.Error("owned WebSocket peer did not drain")
			}
		default:
		}
	})
	go func() { defer close(done); _ = tc.Connect(ctx) }()
	waitFor(t, 2*time.Second, "owned connected fixture", tc.IsConnected)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("post-ACK actual delete never entered")
	}
	select {
	case <-ackRead:
	case <-time.After(2 * time.Second):
		t.Fatal("actual decommission ACK not read by peer")
	}
	select {
	case <-done:
		t.Fatal("tunnel returned with actual owned DELETE still held")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled tunnel did not join owned delete")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("API delete did not stop with owner cancellation")
	}
	if h.selfDeletePending.Load() {
		t.Fatal("joined writer stranded self-delete ownership")
	}
}
