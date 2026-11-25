package vmhost_test

import (
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A std function given a value past what its Go side holds stops the
// program with the function's own text at the call's line: the program's
// fault, never a machine limit (a host adapter that recovered the trap
// reported it as one) and never a wrapped answer.
func TestRun_AValuePastAStdRangeIsTheProgramsFault(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"Decimal scale", `import
    std::io
    std::decimal.RoundingMode
end

fn main()
    io.print(Decimal.round(1.5d, 4294967298, RoundingMode.HalfEven))
end
`, "line 7: Decimal.round: scale 4294967298 is past a Decimal's, which is -2147483648 to 2147483647"},
		{"TLS cipher suite", `import
    std::http.Client
    std::io
    std::tls
end

fn main()
    case Client.with_tls(Client.new(), tls.ClientConfig{cipher_suites: Some([0x1C02F])})
        Ok(_)  -> io.print("built")
        Err(e) -> io.print(e)
    end
end
`, "line 8: tls: cipher suite 0x1C02F is past a suite ID's 16 bits, 0x0000 to 0xFFFF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runVM(t, c.src)
			if b, blocked := vmhost.IsBlocked(err); blocked {
				t.Fatalf("reported as the VM's limit: %q", b.Reasons)
			}
			if err == nil || err.Error() != c.want {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}
