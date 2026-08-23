package cuptitrace

import (
	"os/exec"
	"strings"
	"sync"
)

// demangle converts an Itanium-mangled kernel symbol into a readable name.
// It shells out to c++filt (binutils, present on essentially all Linux
// systems with a CUDA toolchain) and falls back to the raw symbol when
// c++filt is unavailable or rejects the input. Results are memoized because
// GPU workloads launch the same kernels thousands of times.
var (
	demangleMu     sync.Mutex
	demangleCache  = make(map[string]string)
	demangleFilt   string
	demangleLookup sync.Once
)

func cxxfiltPath() string {
	demangleLookup.Do(func() {
		p, err := exec.LookPath("c++filt")
		if err != nil {
			p = ""
		}
		demangleFilt = p
	})
	return demangleFilt
}

// Demangle returns a readable form of a mangled C++ symbol.
func Demangle(symbol string) string {
	if !strings.HasPrefix(symbol, "_Z") {
		return symbol
	}
	demangleMu.Lock()
	defer demangleMu.Unlock()
	if cached, ok := demangleCache[symbol]; ok {
		return cached
	}
	name := symbol
	if filt := cxxfiltPath(); filt != "" {
		if out, err := exec.Command(filt, symbol).Output(); err == nil {
			name = strings.TrimSpace(string(out))
		}
	}
	// Keep only the qualified function name; the full template argument list
	// is retained but signature parameter types after "(" are dropped for
	// track readability.
	if i := strings.Index(name, "("); i > 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), ")"))
	if name == "" || name == symbol {
		name = symbol // fall back when c++filt cannot demangle
	}
	demangleCache[symbol] = name
	return name
}

// shortName collapses a demangled template instantiation to its class-function
// stem: "mlx::core::cu::qmv_kernel<...>" keeps the mlx::core::cu::qmv_kernel
// prefix plus a bounded template head, so Perfetto track names stay readable.
func ShortName(demangled string) string {
	const maxLen = 96
	if len(demangled) <= maxLen {
		return demangled
	}
	return demangled[:maxLen-3] + "..."
}
