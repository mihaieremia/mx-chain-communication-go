//go:build !linux

package afxdp

import "errors"

// ErrNotLinux is returned on non-Linux platforms
var ErrNotLinux = errors.New("AF_XDP is only supported on Linux")

// UMEM stub for non-Linux
type UMEM struct{}

func NewUMEM(_ UMEMConfig) (*UMEM, error) {
	return nil, ErrNotLinux
}

func (u *UMEM) Register(_ int) error          { return ErrNotLinux }
func (u *UMEM) AllocFrame() (uint64, bool)    { return 0, false }
func (u *UMEM) AllocFrames(_ int) ([]uint64, int) { return nil, 0 }
func (u *UMEM) FreeFrame(_ uint64) bool       { return false }
func (u *UMEM) FreeFrames(_ []uint64)         {}
func (u *UMEM) GetFrame(_ uint64) []byte      { return nil }
func (u *UMEM) GetFrameData(_ uint64) []byte  { return nil }
func (u *UMEM) WriteToFrame(_ uint64, _ []byte) int { return 0 }
func (u *UMEM) ReadFromFrame(_ uint64, _ uint32) []byte { return nil }
func (u *UMEM) FreeCount() int                { return 0 }
func (u *UMEM) FrameSize() uint32             { return 0 }
func (u *UMEM) NumFrames() uint32             { return 0 }
func (u *UMEM) Headroom() uint32              { return 0 }
func (u *UMEM) Close() error                  { return nil }

// Socket stub for non-Linux
type Socket struct{}

func New(_ Config) (*Socket, error) {
	return nil, ErrNotLinux
}

func (s *Socket) Receive(_ [][]byte) (int, error)  { return 0, ErrNotLinux }
func (s *Socket) ReceiveOne() ([]byte, error)      { return nil, ErrNotLinux }
func (s *Socket) Send(_ [][]byte) (int, error)     { return 0, ErrNotLinux }
func (s *Socket) SendOne(_ []byte) error           { return ErrNotLinux }
func (s *Socket) Poll(_ int) (bool, bool, error)   { return false, false, ErrNotLinux }
func (s *Socket) Fd() int                          { return -1 }
func (s *Socket) InterfaceIndex() int              { return -1 }
func (s *Socket) QueueID() int                     { return -1 }
func (s *Socket) GetStats() Stats                  { return Stats{} }
func (s *Socket) Close() error                     { return nil }

// XDPProgram stub for non-Linux
type XDPProgram struct{}

func NewXDPProgram(_ string, _ XDPMode) (*XDPProgram, error) {
	return nil, ErrNotLinux
}

func (p *XDPProgram) RegisterSocket(_ int, _ int) error   { return ErrNotLinux }
func (p *XDPProgram) UnregisterSocket(_ int) error        { return ErrNotLinux }
func (p *XDPProgram) Close() error                        { return nil }
func (p *XDPProgram) IsNativeMode() bool                  { return false }

func LoadXDPProgramFromFile(_ string, _ string, _ XDPMode) (*XDPProgram, error) {
	return nil, ErrNotLinux
}

// Manager stub for non-Linux
type Manager struct{}

func NewManager(_ ManagerConfig) (*Manager, error) {
	return nil, ErrNotLinux
}

func (m *Manager) Start(_ func(int, []byte)) error { return ErrNotLinux }
func (m *Manager) Send(_ []byte) error             { return ErrNotLinux }
func (m *Manager) SendToQueue(_ int, _ []byte) error { return ErrNotLinux }
func (m *Manager) SendBatch(_ [][]byte) (int, error) { return 0, ErrNotLinux }
func (m *Manager) GetStats() ManagerStats          { return ManagerStats{} }
func (m *Manager) Stop()                           {}
func (m *Manager) Close() error                    { return nil }

// Helper functions stubs
func GetNumQueues(_ string) (int, error)          { return 0, ErrNotLinux }
func SetNumQueues(_ string, _ int) error          { return ErrNotLinux }
func GetInterfaceInfo(_ string) (*InterfaceInfo, error) { return nil, ErrNotLinux }
func GetDriverInfo(_ string) (*DriverInfo, error) { return nil, ErrNotLinux }
func GetIRQInfo(_ string) ([]IRQInfo, error)      { return nil, ErrNotLinux }
func BenchmarkSocket(_ *Socket, _ int, _ int) (uint64, uint64, error) {
	return 0, 0, ErrNotLinux
}

// Ring stubs
type Ring struct{}
type FillRing struct{ Ring }
type CompletionRing struct{ Ring }
type RxRing struct{ Ring }
type TxRing struct{ Ring }

func NewFillRing(_ int, _ uint32) (*FillRing, error) { return nil, ErrNotLinux }
func NewCompletionRing(_ int, _ uint32) (*CompletionRing, error) { return nil, ErrNotLinux }
func NewRxRing(_ int, _ uint32) (*RxRing, error) { return nil, ErrNotLinux }
func NewTxRing(_ int, _ uint32) (*TxRing, error) { return nil, ErrNotLinux }
func (r *Ring) Close() error { return nil }
