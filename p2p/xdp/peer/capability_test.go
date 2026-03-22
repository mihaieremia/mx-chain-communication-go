package peer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapability_Encode(t *testing.T) {
	t.Parallel()

	cap := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: make([]byte, 32),
	}

	// Fill public key with test data
	for i := range cap.PublicKey {
		cap.PublicKey[i] = byte(i)
	}

	encoded := cap.Encode()

	// Expected size: 1 + 1 + 2 + 4 + 2 + 32 = 42 bytes
	assert.Len(t, encoded, 42)
	assert.Equal(t, byte(1), encoded[0]) // Supported
	assert.Equal(t, byte(1), encoded[1]) // Version
}

func TestCapability_DecodeCapability(t *testing.T) {
	t.Parallel()

	original := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: make([]byte, 32),
	}

	for i := range original.PublicKey {
		original.PublicKey[i] = byte(i)
	}

	encoded := original.Encode()
	decoded, err := DecodeCapability(encoded)

	require.NoError(t, err)
	assert.Equal(t, original.Supported, decoded.Supported)
	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.UDPPort, decoded.UDPPort)
	assert.Equal(t, original.Features, decoded.Features)
	assert.Equal(t, original.PublicKey, decoded.PublicKey)
}

func TestCapability_DecodeCapability_NotSupported(t *testing.T) {
	t.Parallel()

	cap := &Capability{
		Supported: false,
		Version:   1,
		UDPPort:   0,
		Features:  0,
		PublicKey: []byte{},
	}

	encoded := cap.Encode()
	decoded, err := DecodeCapability(encoded)

	require.NoError(t, err)
	assert.False(t, decoded.Supported)
}

func TestDecodeCapability_TooShort(t *testing.T) {
	t.Parallel()

	data := make([]byte, 5) // Too short

	_, err := DecodeCapability(data)

	require.Error(t, err)
}

func TestDecodeCapability_ShortPublicKey(t *testing.T) {
	t.Parallel()

	// Create valid header but claim a larger public key than exists
	data := make([]byte, 12)
	data[0] = 1 // Supported
	data[1] = 1 // Version
	// Port is bytes 2-3
	// Features is bytes 4-7
	// PubKey length is bytes 8-9, set to 32
	data[8] = 0
	data[9] = 32 // Claims 32 bytes but only 2 bytes follow

	_, err := DecodeCapability(data)

	require.Error(t, err)
}

func TestCapability_FeatureFlags(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uint32(1), FeatureBasic)
	assert.Equal(t, uint32(2), FeatureFragments)
	assert.Equal(t, uint32(4), FeatureCompression)
	assert.Equal(t, uint32(8), FeatureEncryption)
}

func TestExtractIPFromMultiaddr_IPv4(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		maStr    string
		expected string
	}{
		{
			name:     "simple ipv4",
			maStr:    "/ip4/192.168.1.1/tcp/4001",
			expected: "192.168.1.1",
		},
		{
			name:     "localhost",
			maStr:    "/ip4/127.0.0.1/tcp/37373",
			expected: "127.0.0.1",
		},
		{
			name:     "with quic",
			maStr:    "/ip4/10.0.0.1/udp/4001/quic",
			expected: "10.0.0.1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ip := extractIPFromMultiaddr(tc.maStr)
			require.NotNil(t, ip)
			assert.Equal(t, tc.expected, ip.String())
		})
	}
}

func TestExtractIPFromMultiaddr_IPv6(t *testing.T) {
	t.Parallel()

	maStr := "/ip6/::1/tcp/4001"
	ip := extractIPFromMultiaddr(maStr)

	require.NotNil(t, ip)
	assert.Equal(t, "::1", ip.String())
}

func TestExtractIPFromMultiaddr_NoIP(t *testing.T) {
	t.Parallel()

	maStr := "/dns4/example.com/tcp/4001"
	ip := extractIPFromMultiaddr(maStr)

	assert.Nil(t, ip)
}

func TestSplitMultiaddr(t *testing.T) {
	t.Parallel()

	parts := splitMultiaddr("/ip4/192.168.1.1/tcp/4001/p2p/QmTest")

	expected := []string{"ip4", "192.168.1.1", "tcp", "4001", "p2p", "QmTest"}
	assert.Equal(t, expected, parts)
}

func TestSplitMultiaddr_Empty(t *testing.T) {
	t.Parallel()

	parts := splitMultiaddr("")

	assert.Empty(t, parts)
}

func TestSplitMultiaddr_TrailingSlash(t *testing.T) {
	t.Parallel()

	parts := splitMultiaddr("/ip4/127.0.0.1/")

	expected := []string{"ip4", "127.0.0.1"}
	assert.Equal(t, expected, parts)
}

func TestCapability_EncodeDecodeRoundtrip(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		cap  *Capability
	}{
		{
			name: "full capability",
			cap: &Capability{
				Supported: true,
				Version:   1,
				UDPPort:   37374,
				Features:  FeatureBasic | FeatureFragments,
				PublicKey: []byte("12345678901234567890123456789012"),
			},
		},
		{
			name: "not supported",
			cap: &Capability{
				Supported: false,
				Version:   0,
				UDPPort:   0,
				Features:  0,
				PublicKey: []byte{},
			},
		},
		{
			name: "all features",
			cap: &Capability{
				Supported: true,
				Version:   2,
				UDPPort:   65535,
				Features:  FeatureBasic | FeatureFragments | FeatureCompression | FeatureEncryption,
				PublicKey: []byte("short"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.cap.Encode()
			decoded, err := DecodeCapability(encoded)

			require.NoError(t, err)
			assert.Equal(t, tc.cap.Supported, decoded.Supported)
			assert.Equal(t, tc.cap.Version, decoded.Version)
			assert.Equal(t, tc.cap.UDPPort, decoded.UDPPort)
			assert.Equal(t, tc.cap.Features, decoded.Features)
			assert.Equal(t, tc.cap.PublicKey, decoded.PublicKey)
		})
	}
}

// Benchmarks
func BenchmarkCapability_Encode(b *testing.B) {
	cap := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: make([]byte, 32),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cap.Encode()
	}
}

func BenchmarkDecodeCapability(b *testing.B) {
	cap := &Capability{
		Supported: true,
		Version:   1,
		UDPPort:   37374,
		Features:  FeatureBasic | FeatureFragments,
		PublicKey: make([]byte, 32),
	}
	encoded := cap.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecodeCapability(encoded)
	}
}

func BenchmarkExtractIPFromMultiaddr(b *testing.B) {
	maStr := "/ip4/192.168.1.1/tcp/4001/p2p/QmTest"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractIPFromMultiaddr(maStr)
	}
}
