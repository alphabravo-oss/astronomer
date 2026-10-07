package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Both production composition roots must inject the same subscriber they run.
// This source contract complements the live fake-informer integration test and
// avoids starting HTTP/WebSocket listeners merely to inspect dependency wiring.
func TestDeliveryCompositionSharesSubscriberBeforeObserverStart(t *testing.T) {
	for _, path := range []string{"../../cmd/agent/main.go", "../server/localcluster.go"} {
		t.Run(path, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			constructed, injected, run, health, assignmentRuntime := 0, 0, 0, 0, 0
			ast.Inspect(file, func(node ast.Node) bool {
				if goStatement, ok := node.(*ast.GoStmt); ok {
					ast.Inspect(goStatement.Call, func(child ast.Node) bool {
						if call, ok := child.(*ast.CallExpr); ok && selectorName(call) == "NewStateSubscriber" {
							t.Error("subscriber constructed inside goroutine instead of shared composition")
						}
						return true
					})
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch selectorName(call) {
				case "NewObservedDeliveryRuntime":
					if len(call.Args) == 4 {
						client, clientOK := call.Args[1].(*ast.Ident)
						probe, probeOK := call.Args[3].(*ast.Ident)
						if clientOK && probeOK && client.Name == "deliveryDynamic" && probe.Name == "deliveryProbe" {
							assignmentRuntime++
						}
					}
				case "NewStateSubscriber":
					constructed++
				case "WithObservationSource":
					if len(call.Args) == 1 {
						if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "subscriber" {
							injected++
						}
					}
				case "SetInventorySource":
					if len(call.Args) == 1 {
						if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "subscriber" {
							health++
						}
					}
				case "Run":
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
						if id, ok := selector.X.(*ast.Ident); ok && id.Name == "subscriber" {
							run++
						}
					}
				}
				// Embedded runtime passes subscriber.Run as a named-loop callback.
				return true
			})
			if path == "../server/localcluster.go" {
				ast.Inspect(file, func(node ast.Node) bool {
					if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Run" {
						if id, ok := selector.X.(*ast.Ident); ok && id.Name == "subscriber" {
							run++
						}
					}
					return true
				})
			}
			if constructed != 1 || injected != 1 || health != 1 || run != 1 || assignmentRuntime != 1 {
				t.Fatalf("wiring: constructors=%d delivery=%d health=%d run=%d assignmentRuntime=%d", constructed, injected, health, run, assignmentRuntime)
			}
		})
	}
}
func selectorName(call *ast.CallExpr) string {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return ""
}
