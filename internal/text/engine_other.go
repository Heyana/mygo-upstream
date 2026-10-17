//go:build (android && !cgo) || (ios && !cgo) || (!(windows && (amd64 || arm64)) && !darwin && !(linux && (amd64 || arm64)) && !js)

package text

func newEngine() engine { return &stubEngine{} }
