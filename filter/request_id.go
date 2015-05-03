package filter

import (
	"sync"

	"github.com/cosiner/gohper/defval"
	"github.com/cosiner/gohper/errors"
	"github.com/cosiner/zerver"
	"github.com/cosiner/zerver/component"
	"github.com/garyburd/redigo/redis"
)

const (
	ErrRequestIDExist = errors.Err("Request id already exist")
)

type (
	// RequestId is a simple filter prevent application/user from overlap request
	// the request id is generated by client itself or other server components.
	RequestId struct {
		Store         IDStore
		HeaderName    string
		PassingOnNoId bool
		Error         string
		ErrorOverlap  string
		logger        zerver.Logger
	}

	IDStore interface {
		zerver.Component
		// if ip-id pair already exist, return ErrRequestIDExist
		Save(id string) error
		Remove(id string) error
	}

	MemIDStore struct {
		requests map[string]struct{} // [ip:id]exist
		lock     sync.RWMutex
	}

	// RedisIDStore depends on component.Redis
	RedisIDStore struct {
		Key   string // key for redis set to store ip-id pair, default use "RequestID"
		redis *component.Redis
	}
)

func (m *MemIDStore) Init(zerver.Enviroment) error {
	m.requests = make(map[string]struct{})
	m.lock = sync.RWMutex{}
	return nil
}

func (m *MemIDStore) Destroy() {
	m.requests = nil
}

func (m *MemIDStore) Save(id string) (err error) {
	m.lock.Lock()
	if _, has := m.requests[id]; has {
		err = ErrRequestIDExist
	} else {
		m.requests[id] = struct{}{}

	}
	m.lock.Unlock()
	return
}

func (m *MemIDStore) Remove(id string) error {
	m.lock.Lock()
	delete(m.requests, id)
	m.lock.Unlock()
	return nil
}

func (r *RedisIDStore) Init(env zerver.Enviroment) error {
	redis, err := env.Component(component.COMP_REDIS)
	if err == nil {
		if redis == nil {
			err = errors.Err("component redis isn't loaded")
		} else {
			r.redis = redis.(*component.Redis)
			defval.String(&r.Key, "RequestID")
		}
	}
	return err
}

func (r *RedisIDStore) Destroy() {
	r.redis = nil
}

func (r *RedisIDStore) Save(id string) error {
	success, err := redis.Bool(r.redis.Exec("SADD", r.Key, id))
	if err == nil && !success {
		err = ErrRequestIDExist
	}
	return err
}

func (r *RedisIDStore) Remove(ip, id string) error {
	_, err := r.redis.Exec("SREM", r.Key, id)
	return err
}

func (ri *RequestId) Init(env zerver.Enviroment) error {
	defval.Nil(&ri.Store, new(MemIDStore))
	ri.Store.Init(env)
	defval.String(&ri.HeaderName, "X-Request-Id")
	defval.String(&ri.Error, "header value X-Request-Id can't be empty")
	defval.String(&ri.ErrorOverlap, "request already accepted before, please wait")
	ri.logger = env.Logger()
	return nil
}

func (ri *RequestId) Filter(req zerver.Request, resp zerver.Response, chain zerver.FilterChain) {
	if req.Method() == "GET" {
		chain(req, resp)
		return
	}
	reqId := req.Header(ri.HeaderName)
	if reqId == "" {
		if ri.PassingOnNoId {
			chain(req, resp)
		} else {
			resp.ReportBadRequest()
			resp.Send("error", ri.Error)
		}
	} else {
		id := req.RemoteIP() + ":" + reqId
		if err := ri.Store.Save(id); err == ErrRequestIDExist {
			resp.ReportForbidden()
			resp.Send("error", ri.ErrorOverlap)
		} else if err != nil {
			ri.logger.Panicln(err)
		} else {
			chain(req, resp)
			ri.Store.Remove(id)
		}
	}
}

func (ri *RequestId) Destroy() {}