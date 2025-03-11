package tglog

import "time"

// WithAppID sets AppID
func WithAppID(id string) Option {
	return func(o *Options) {
		if id == "" {
			return
		}
		o.AppID = id
	}
}

// WithAppName sets AppName
func WithAppName(n string) Option {
	return func(o *Options) {
		if n == "" {
			return
		}
		o.AppName = n
	}
}

// WithAppVer sets AppVer
func WithAppVer(v string) Option {
	return func(o *Options) {
		if v == "" {
			return
		}
		o.AppVer = v
	}
}

// WithSendTimeout sets SendTimeout
func WithSendTimeout(t time.Duration) Option {
	return func(o *Options) {
		if t <= 0 {
			return
		}
		o.SendTimeout = t
	}
}

// WithMaxRetries sets MaxRetries
func WithMaxRetries(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.MaxRetries = n
	}
}

// WithCompress sets Compress
func WithCompress(c bool) Option {
	return func(o *Options) {
		o.Compress = c
	}
}

// WithEncrypt sets Encrypt
func WithEncrypt(e bool) Option {
	return func(o *Options) {
		o.Encrypt = e
	}
}

// WithEncryptKey sets EncryptKey
func WithEncryptKey(k string) Option {
	return func(o *Options) {
		if k == "" {
			return
		}
		o.EncryptKey = k
	}
}

// WithNoFrameHeader sets NoFrameHeader
func WithNoFrameHeader(n bool) Option {
	return func(o *Options) {
		o.NoFrameHeader = n
	}
}

// WithLittleEndian sets LittleEndian
func WithLittleEndian(e bool) Option {
	return func(o *Options) {
		o.LittleEndian = e
	}
}

// WithMaxFrameLen sets MaxFrameLen
func WithMaxFrameLen(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.MaxFrameLen = n
	}
}

// WithLenFieldOffset sets LenFieldOffset
func WithLenFieldOffset(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.LenFieldOffset = n
	}
}

// WithLenFieldLength sets LenFieldLength
func WithLenFieldLength(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.LenFieldLength = n
	}
}

// WithLenAdjustment sets LenAdjustment
func WithLenAdjustment(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.LenAdjustment = n
	}
}

// WithFrameBytesToStrip sets FrameBytesToStrip
func WithFrameBytesToStrip(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.FrameBytesToStrip = n
	}
}

// WithPayloadBytesToTrip sets PayloadBytesToTrip
func WithPayloadBytesToTrip(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.PayloadBytesToTrip = n
	}
}

// WithToken sets Token
func WithToken(t string) Option {
	return func(o *Options) {
		if t == "" {
			return
		}
		o.Token = t
	}
}

// WithTokenType sets TokenType
func WithTokenType(t string) Option {
	return func(o *Options) {
		if t == "" {
			return
		}
		o.TokenType = t
	}
}

// WithSign sets Sign
func WithSign(s bool) Option {
	return func(o *Options) {
		o.Sign = s
	}
}

// WithAuth sets auth
func WithAuth(a bool) Option {
	return func(o *Options) {
		o.Auth = a
	}
}
