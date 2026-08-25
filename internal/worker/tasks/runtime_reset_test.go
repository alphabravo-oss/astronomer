package tasks

import "context"

func testRuntimeContext(deps RuntimeDependencies) context.Context {
	return (CoreRuntime{Deps: deps}).Context(context.Background())
}
