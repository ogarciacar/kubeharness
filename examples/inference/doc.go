// Package inference holds the kronk image and the routing oracle for K01,
// whose goal is for AgentGateway to route inference to one kronk Pod that
// serves three open-weight models. The oracle counts a request as routed only
// when the reply names the pinned model id and kronk's own request counter for
// that model, and no other, moved, so a crossed route fails.
//
// ./kronk builds kubeharness.local/kronk-k01:test with the weights, the
// libraries and the model config inside, so the Pod fetches nothing. The
// first build downloads the base image and three GGUF files, each checked
// against its sha256.
//
//	docker build -t kubeharness.local/kronk-k01:test kronk
//
// The oracle tests need no image.
//
//	go test -count=1 -v .
package inference
