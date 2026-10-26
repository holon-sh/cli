package mcptype

import "testing"

func TestNumbersRoundTripAsSerdeWrites(t *testing.T) {
	for literal, want := range map[string]string{
		"1": "1", "-1": "-1", "0": "0", "-0": "-0.0", "1.0": "1.0", "1.5": "1.5", "1e2": "100.0", "1E2": "100.0", "0.1": "0.1",
		"1e16": "1e+16", "1e15": "1000000000000000.0", "1234e7": "12340000000.0", "1e21": "1e+21", "1.5e-7": "1.5e-7", "0.001234": "0.001234", "0.00001234": "0.00001234", "0.000001234": "1.234e-6",
		"18446744073709551615": "18446744073709551615", "18446744073709551616": "1.8446744073709552e+19", "-9223372036854775808": "-9223372036854775808", "-9223372036854775809": "-9.223372036854776e+18",
		"3.141592653589793": "3.141592653589793", "123456789012345680000": "1.2345678901234568e+20",
	} {
		number, err := parseNumber(literal)
		if err != nil {
			t.Fatal(err)
		}
		if got := number.String(); got != want {
			t.Errorf("%s: got %s, want %s", literal, got, want)
		}
	}
	if _, err := parseNumber("1e400"); err == nil {
		t.Error("accepted an out-of-range float")
	}
}

func TestStringsAreQuotedAsSerdeWrites(t *testing.T) {
	if got, want := quoted("a\"b\\c\n\r\t\b\f\x01\x1f\x7f<>&é\u2028"), `"a\"b\\c\n\r\t\b\f\u0001\u001f`+"\x7f<>&é\u2028"+`"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := pythonDumps(Object{"a": []Value{"é😀", Number{kind: posInt, u: 1}, Number{kind: float, f: 2.5}, nil, true}, "b'": "\\\n"}), `{"a": ["\u00e9\ud83d\ude00", 1, 2.5, null, true], "b'": "\\\n"}`; got != want {
		t.Errorf("dumps: got %s, want %s", got, want)
	}
	if got := pythonRepr(`{"b'":"\\n"}`); got != `'{"b\'":"\\\\n"}'` {
		t.Errorf("repr: got %s", got)
	}
	if got := pythonRepr("it's"); got != `"it's"` {
		t.Errorf("repr: got %s", got)
	}
}
