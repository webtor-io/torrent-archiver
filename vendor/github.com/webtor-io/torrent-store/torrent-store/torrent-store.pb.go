// Code generated by protoc-gen-go. DO NOT EDIT.
// source: torrent-store.proto

package torrent_store

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"

import (
	context "golang.org/x/net/context"
	grpc "google.golang.org/grpc"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

// The push response message containing info hash of the pushed torrent file
type PushReply struct {
	InfoHash             string   `protobuf:"bytes,1,opt,name=infoHash,proto3" json:"infoHash,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *PushReply) Reset()         { *m = PushReply{} }
func (m *PushReply) String() string { return proto.CompactTextString(m) }
func (*PushReply) ProtoMessage()    {}
func (*PushReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{0}
}
func (m *PushReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PushReply.Unmarshal(m, b)
}
func (m *PushReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PushReply.Marshal(b, m, deterministic)
}
func (dst *PushReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PushReply.Merge(dst, src)
}
func (m *PushReply) XXX_Size() int {
	return xxx_messageInfo_PushReply.Size(m)
}
func (m *PushReply) XXX_DiscardUnknown() {
	xxx_messageInfo_PushReply.DiscardUnknown(m)
}

var xxx_messageInfo_PushReply proto.InternalMessageInfo

func (m *PushReply) GetInfoHash() string {
	if m != nil {
		return m.InfoHash
	}
	return ""
}

// The push request message containing the torrent and expire duration is seconds
type PushRequest struct {
	Torrent              []byte   `protobuf:"bytes,1,opt,name=torrent,proto3" json:"torrent,omitempty"`
	Expire               int32    `protobuf:"varint,2,opt,name=expire,proto3" json:"expire,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *PushRequest) Reset()         { *m = PushRequest{} }
func (m *PushRequest) String() string { return proto.CompactTextString(m) }
func (*PushRequest) ProtoMessage()    {}
func (*PushRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{1}
}
func (m *PushRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PushRequest.Unmarshal(m, b)
}
func (m *PushRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PushRequest.Marshal(b, m, deterministic)
}
func (dst *PushRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PushRequest.Merge(dst, src)
}
func (m *PushRequest) XXX_Size() int {
	return xxx_messageInfo_PushRequest.Size(m)
}
func (m *PushRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_PushRequest.DiscardUnknown(m)
}

var xxx_messageInfo_PushRequest proto.InternalMessageInfo

func (m *PushRequest) GetTorrent() []byte {
	if m != nil {
		return m.Torrent
	}
	return nil
}

func (m *PushRequest) GetExpire() int32 {
	if m != nil {
		return m.Expire
	}
	return 0
}

// The pull request message containing the infoHash
type PullRequest struct {
	InfoHash             string   `protobuf:"bytes,1,opt,name=infoHash,proto3" json:"infoHash,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *PullRequest) Reset()         { *m = PullRequest{} }
func (m *PullRequest) String() string { return proto.CompactTextString(m) }
func (*PullRequest) ProtoMessage()    {}
func (*PullRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{2}
}
func (m *PullRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PullRequest.Unmarshal(m, b)
}
func (m *PullRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PullRequest.Marshal(b, m, deterministic)
}
func (dst *PullRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PullRequest.Merge(dst, src)
}
func (m *PullRequest) XXX_Size() int {
	return xxx_messageInfo_PullRequest.Size(m)
}
func (m *PullRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_PullRequest.DiscardUnknown(m)
}

var xxx_messageInfo_PullRequest proto.InternalMessageInfo

func (m *PullRequest) GetInfoHash() string {
	if m != nil {
		return m.InfoHash
	}
	return ""
}

// The pull response message containing the torrent
type PullReply struct {
	Torrent              []byte   `protobuf:"bytes,1,opt,name=torrent,proto3" json:"torrent,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *PullReply) Reset()         { *m = PullReply{} }
func (m *PullReply) String() string { return proto.CompactTextString(m) }
func (*PullReply) ProtoMessage()    {}
func (*PullReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{3}
}
func (m *PullReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PullReply.Unmarshal(m, b)
}
func (m *PullReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PullReply.Marshal(b, m, deterministic)
}
func (dst *PullReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PullReply.Merge(dst, src)
}
func (m *PullReply) XXX_Size() int {
	return xxx_messageInfo_PullReply.Size(m)
}
func (m *PullReply) XXX_DiscardUnknown() {
	xxx_messageInfo_PullReply.DiscardUnknown(m)
}

var xxx_messageInfo_PullReply proto.InternalMessageInfo

func (m *PullReply) GetTorrent() []byte {
	if m != nil {
		return m.Torrent
	}
	return nil
}

// The check request message containing the infoHash
type CheckRequest struct {
	InfoHash             string   `protobuf:"bytes,1,opt,name=infoHash,proto3" json:"infoHash,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *CheckRequest) Reset()         { *m = CheckRequest{} }
func (m *CheckRequest) String() string { return proto.CompactTextString(m) }
func (*CheckRequest) ProtoMessage()    {}
func (*CheckRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{4}
}
func (m *CheckRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CheckRequest.Unmarshal(m, b)
}
func (m *CheckRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CheckRequest.Marshal(b, m, deterministic)
}
func (dst *CheckRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CheckRequest.Merge(dst, src)
}
func (m *CheckRequest) XXX_Size() int {
	return xxx_messageInfo_CheckRequest.Size(m)
}
func (m *CheckRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_CheckRequest.DiscardUnknown(m)
}

var xxx_messageInfo_CheckRequest proto.InternalMessageInfo

func (m *CheckRequest) GetInfoHash() string {
	if m != nil {
		return m.InfoHash
	}
	return ""
}

// The check response message containing existance flag
type CheckReply struct {
	Exists               bool     `protobuf:"varint,1,opt,name=exists,proto3" json:"exists,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *CheckReply) Reset()         { *m = CheckReply{} }
func (m *CheckReply) String() string { return proto.CompactTextString(m) }
func (*CheckReply) ProtoMessage()    {}
func (*CheckReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{5}
}
func (m *CheckReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CheckReply.Unmarshal(m, b)
}
func (m *CheckReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CheckReply.Marshal(b, m, deterministic)
}
func (dst *CheckReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CheckReply.Merge(dst, src)
}
func (m *CheckReply) XXX_Size() int {
	return xxx_messageInfo_CheckReply.Size(m)
}
func (m *CheckReply) XXX_DiscardUnknown() {
	xxx_messageInfo_CheckReply.DiscardUnknown(m)
}

var xxx_messageInfo_CheckReply proto.InternalMessageInfo

func (m *CheckReply) GetExists() bool {
	if m != nil {
		return m.Exists
	}
	return false
}

// The touch response message
type TouchReply struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *TouchReply) Reset()         { *m = TouchReply{} }
func (m *TouchReply) String() string { return proto.CompactTextString(m) }
func (*TouchReply) ProtoMessage()    {}
func (*TouchReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{6}
}
func (m *TouchReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TouchReply.Unmarshal(m, b)
}
func (m *TouchReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TouchReply.Marshal(b, m, deterministic)
}
func (dst *TouchReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TouchReply.Merge(dst, src)
}
func (m *TouchReply) XXX_Size() int {
	return xxx_messageInfo_TouchReply.Size(m)
}
func (m *TouchReply) XXX_DiscardUnknown() {
	xxx_messageInfo_TouchReply.DiscardUnknown(m)
}

var xxx_messageInfo_TouchReply proto.InternalMessageInfo

// The touch request message containing the torrent and expire duration is seconds
type TouchRequest struct {
	InfoHash             string   `protobuf:"bytes,1,opt,name=infoHash,proto3" json:"infoHash,omitempty"`
	Expire               int32    `protobuf:"varint,2,opt,name=expire,proto3" json:"expire,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *TouchRequest) Reset()         { *m = TouchRequest{} }
func (m *TouchRequest) String() string { return proto.CompactTextString(m) }
func (*TouchRequest) ProtoMessage()    {}
func (*TouchRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_torrent_store_645a42438724e3b6, []int{7}
}
func (m *TouchRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TouchRequest.Unmarshal(m, b)
}
func (m *TouchRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TouchRequest.Marshal(b, m, deterministic)
}
func (dst *TouchRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TouchRequest.Merge(dst, src)
}
func (m *TouchRequest) XXX_Size() int {
	return xxx_messageInfo_TouchRequest.Size(m)
}
func (m *TouchRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_TouchRequest.DiscardUnknown(m)
}

var xxx_messageInfo_TouchRequest proto.InternalMessageInfo

func (m *TouchRequest) GetInfoHash() string {
	if m != nil {
		return m.InfoHash
	}
	return ""
}

func (m *TouchRequest) GetExpire() int32 {
	if m != nil {
		return m.Expire
	}
	return 0
}

func init() {
	proto.RegisterType((*PushReply)(nil), "PushReply")
	proto.RegisterType((*PushRequest)(nil), "PushRequest")
	proto.RegisterType((*PullRequest)(nil), "PullRequest")
	proto.RegisterType((*PullReply)(nil), "PullReply")
	proto.RegisterType((*CheckRequest)(nil), "CheckRequest")
	proto.RegisterType((*CheckReply)(nil), "CheckReply")
	proto.RegisterType((*TouchReply)(nil), "TouchReply")
	proto.RegisterType((*TouchRequest)(nil), "TouchRequest")
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// TorrentStoreClient is the client API for TorrentStore service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type TorrentStoreClient interface {
	// Pushes torrent to the store
	Push(ctx context.Context, in *PushRequest, opts ...grpc.CallOption) (*PushReply, error)
	// Pulls torrent from the store
	Pull(ctx context.Context, in *PullRequest, opts ...grpc.CallOption) (*PullReply, error)
	// Check torrent in the store for existence
	Check(ctx context.Context, in *CheckRequest, opts ...grpc.CallOption) (*CheckReply, error)
	// Touch torrent in the store
	Touch(ctx context.Context, in *TouchRequest, opts ...grpc.CallOption) (*TouchReply, error)
}

type torrentStoreClient struct {
	cc *grpc.ClientConn
}

func NewTorrentStoreClient(cc *grpc.ClientConn) TorrentStoreClient {
	return &torrentStoreClient{cc}
}

func (c *torrentStoreClient) Push(ctx context.Context, in *PushRequest, opts ...grpc.CallOption) (*PushReply, error) {
	out := new(PushReply)
	err := c.cc.Invoke(ctx, "/TorrentStore/Push", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *torrentStoreClient) Pull(ctx context.Context, in *PullRequest, opts ...grpc.CallOption) (*PullReply, error) {
	out := new(PullReply)
	err := c.cc.Invoke(ctx, "/TorrentStore/Pull", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *torrentStoreClient) Check(ctx context.Context, in *CheckRequest, opts ...grpc.CallOption) (*CheckReply, error) {
	out := new(CheckReply)
	err := c.cc.Invoke(ctx, "/TorrentStore/Check", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *torrentStoreClient) Touch(ctx context.Context, in *TouchRequest, opts ...grpc.CallOption) (*TouchReply, error) {
	out := new(TouchReply)
	err := c.cc.Invoke(ctx, "/TorrentStore/Touch", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TorrentStoreServer is the server API for TorrentStore service.
type TorrentStoreServer interface {
	// Pushes torrent to the store
	Push(context.Context, *PushRequest) (*PushReply, error)
	// Pulls torrent from the store
	Pull(context.Context, *PullRequest) (*PullReply, error)
	// Check torrent in the store for existence
	Check(context.Context, *CheckRequest) (*CheckReply, error)
	// Touch torrent in the store
	Touch(context.Context, *TouchRequest) (*TouchReply, error)
}

func RegisterTorrentStoreServer(s *grpc.Server, srv TorrentStoreServer) {
	s.RegisterService(&_TorrentStore_serviceDesc, srv)
}

func _TorrentStore_Push_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PushRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TorrentStoreServer).Push(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/TorrentStore/Push",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TorrentStoreServer).Push(ctx, req.(*PushRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _TorrentStore_Pull_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PullRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TorrentStoreServer).Pull(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/TorrentStore/Pull",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TorrentStoreServer).Pull(ctx, req.(*PullRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _TorrentStore_Check_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CheckRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TorrentStoreServer).Check(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/TorrentStore/Check",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TorrentStoreServer).Check(ctx, req.(*CheckRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _TorrentStore_Touch_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TouchRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TorrentStoreServer).Touch(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/TorrentStore/Touch",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TorrentStoreServer).Touch(ctx, req.(*TouchRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _TorrentStore_serviceDesc = grpc.ServiceDesc{
	ServiceName: "TorrentStore",
	HandlerType: (*TorrentStoreServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Push",
			Handler:    _TorrentStore_Push_Handler,
		},
		{
			MethodName: "Pull",
			Handler:    _TorrentStore_Pull_Handler,
		},
		{
			MethodName: "Check",
			Handler:    _TorrentStore_Check_Handler,
		},
		{
			MethodName: "Touch",
			Handler:    _TorrentStore_Touch_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "torrent-store.proto",
}

func init() { proto.RegisterFile("torrent-store.proto", fileDescriptor_torrent_store_645a42438724e3b6) }

var fileDescriptor_torrent_store_645a42438724e3b6 = []byte{
	// 268 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x8c, 0x92, 0x51, 0x4b, 0xc3, 0x30,
	0x14, 0x85, 0x5b, 0x71, 0x73, 0xbb, 0xcd, 0x5e, 0x22, 0x48, 0xe9, 0xd3, 0xb8, 0x38, 0x9c, 0x82,
	0x79, 0xd0, 0x1f, 0x20, 0xe8, 0x8b, 0x8f, 0x12, 0xfd, 0x03, 0x3a, 0x22, 0x2d, 0x86, 0xa5, 0x26,
	0x29, 0xe8, 0xff, 0xf1, 0x87, 0xca, 0xbd, 0xcd, 0x6a, 0x5f, 0xa6, 0x3e, 0x1e, 0xf8, 0x38, 0xf7,
	0x9c, 0x93, 0xc0, 0x71, 0x74, 0xde, 0x9b, 0x6d, 0xbc, 0x0c, 0xd1, 0x79, 0xa3, 0x5a, 0xef, 0xa2,
	0xc3, 0x33, 0x98, 0x3f, 0x74, 0xa1, 0xd6, 0xa6, 0xb5, 0x9f, 0xb2, 0x82, 0x59, 0xb3, 0x7d, 0x75,
	0xf7, 0xcf, 0xa1, 0x2e, 0xf3, 0x65, 0xbe, 0x9e, 0xeb, 0x41, 0xe3, 0x0d, 0x14, 0x3d, 0xf8, 0xde,
	0x99, 0x10, 0x65, 0x09, 0x47, 0xc9, 0x8e, 0x49, 0xa1, 0x77, 0x52, 0x9e, 0xc0, 0xd4, 0x7c, 0xb4,
	0x8d, 0x37, 0xe5, 0xc1, 0x32, 0x5f, 0x4f, 0x74, 0x52, 0x78, 0x4e, 0x06, 0xd6, 0xee, 0x0c, 0x7e,
	0xbb, 0xb5, 0xa2, 0x50, 0x84, 0x52, 0xa8, 0xbd, 0x97, 0xf0, 0x02, 0xc4, 0x5d, 0x6d, 0x36, 0x6f,
	0xff, 0xb1, 0x3c, 0x05, 0x48, 0x2c, 0x79, 0x72, 0xc6, 0x26, 0xc4, 0xc0, 0xdc, 0x4c, 0x27, 0x85,
	0x02, 0xe0, 0xc9, 0x75, 0x9b, 0x7e, 0x0e, 0xbc, 0x05, 0x91, 0xd4, 0x9f, 0xfe, 0xfb, 0x5a, 0x5f,
	0x7d, 0xe5, 0x64, 0xc2, 0x79, 0x1f, 0x69, 0x76, 0x89, 0x70, 0x48, 0x3b, 0x4a, 0xa1, 0x46, 0x73,
	0x56, 0xa0, 0x86, 0x57, 0xc0, 0xac, 0x67, 0xac, 0x65, 0x66, 0x58, 0x8c, 0x99, 0x34, 0x0a, 0x66,
	0x72, 0x05, 0x13, 0x2e, 0x24, 0x17, 0x6a, 0x3c, 0x42, 0x55, 0xa8, 0x9f, 0x9e, 0x3d, 0xc6, 0x1d,
	0xe4, 0x42, 0x8d, 0xbb, 0x54, 0x85, 0x1a, 0x15, 0xcd, 0x5e, 0xa6, 0xfc, 0x1b, 0xae, 0xbf, 0x03,
	0x00, 0x00, 0xff, 0xff, 0xe1, 0x3a, 0x12, 0xd8, 0x24, 0x02, 0x00, 0x00,
}
