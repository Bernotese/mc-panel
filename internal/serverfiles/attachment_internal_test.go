package serverfiles

import "testing"

func TestAttachmentHeader(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"server.properties", `attachment; filename="server.properties"; filename*=UTF-8''server.properties`},
		{"Wélt 1.zip", `attachment; filename="W_lt 1.zip"; filename*=UTF-8''W%C3%A9lt%201.zip`},
		{`a"b\c;d.txt`, `attachment; filename="a_b_c_d.txt"; filename*=UTF-8''a%22b%5Cc%3Bd.txt`},
		{"日本.txt", `attachment; filename="__.txt"; filename*=UTF-8''%E6%97%A5%E6%9C%AC.txt`},
		{"x\r\ny", `attachment; filename="x__y"; filename*=UTF-8''x%0D%0Ay`},
		// SEC-38: Unicode format characters (bidi controls, zero-width characters, BOM, soft
		// hyphen) are dropped from both names, so "gpj.exe" with U+202E cannot pose as "exe.jpg".
		{"invoice\u202egpj.exe", `attachment; filename="invoicegpj.exe"; filename*=UTF-8''invoicegpj.exe`},
		{"a\u200bb\u2066c\u2069\ufeff\u00add.txt", `attachment; filename="abcd.txt"; filename*=UTF-8''abcd.txt`},
		{"\u202e\u200b", `attachment; filename="download"; filename*=UTF-8''download`},
	} {
		if got := attachment(tc.name); got != tc.want {
			t.Errorf("attachment(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
}
