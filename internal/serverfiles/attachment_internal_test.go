package serverfiles

import "testing"

func TestAttachmentHeader(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"server.properties", `attachment; filename="server.properties"; filename*=UTF-8''server.properties`},
		{"Wélt 1.zip", `attachment; filename="W_lt 1.zip"; filename*=UTF-8''W%C3%A9lt%201.zip`},
		{`a"b\c;d.txt`, `attachment; filename="a_b_c_d.txt"; filename*=UTF-8''a%22b%5Cc%3Bd.txt`},
		{"日本.txt", `attachment; filename="__.txt"; filename*=UTF-8''%E6%97%A5%E6%9C%AC.txt`},
		{"x\r\ny", `attachment; filename="x__y"; filename*=UTF-8''x%0D%0Ay`},
	} {
		if got := attachment(tc.name); got != tc.want {
			t.Errorf("attachment(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
}
