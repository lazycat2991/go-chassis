package rest

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ServiceComb/go-chassis/core/client"
	"github.com/ServiceComb/go-chassis/core/codec"
	"github.com/ServiceComb/go-chassis/core/loadbalance"
	clientOption "github.com/ServiceComb/go-chassis/third_party/forked/go-micro/client"
	"github.com/ServiceComb/go-chassis/third_party/forked/valyala/fasthttp"
	"golang.org/x/net/context"
)

const (
	// Name is a constant of type string
	Name = "rest"
	// FailureTypePrefix is a constant of type string
	FailureTypePrefix = "http_"
)

//HTTPFailureTypeMap is a variable of type map
var HTTPFailureTypeMap = map[string]bool{
	FailureTypePrefix + strconv.Itoa(http.StatusInternalServerError): true, //http_500
	FailureTypePrefix + strconv.Itoa(http.StatusBadGateway):          true, //http_502
	FailureTypePrefix + strconv.Itoa(http.StatusServiceUnavailable):  true, //http_503
	FailureTypePrefix + strconv.Itoa(http.StatusGatewayTimeout):      true, //http_504
	FailureTypePrefix + strconv.Itoa(http.StatusTooManyRequests):     true, //http_429
}

func init() {
	client.InstallPlugin(Name, NewRestClient)
	loadbalance.LatencyMap = make(map[string][]time.Duration)
}

//NewRestClient is a function
func NewRestClient(options ...clientOption.Option) client.Client {
	opts := clientOption.Options{}
	for _, o := range options {
		o(&opts)
	}

	if opts.Codecs == nil {
		opts.Codecs = codec.GetCodecMap()
	}

	if len(opts.ContentType) == 0 {
		//TODO take effect of that option
		opts.ContentType = "application/json"
	}

	if opts.Failure == nil || len(opts.Failure) == 0 {
		opts.Failure = HTTPFailureTypeMap
	} else {
		tmpFailureMap := make(map[string]bool)
		for k := range opts.Failure {
			if HTTPFailureTypeMap[k] {
				tmpFailureMap[k] = true
			}

		}

		opts.Failure = tmpFailureMap
	}

	poolSize := 1000
	if opts.PoolSize != 0 {
		poolSize = opts.PoolSize
	}

	rc := &Client{
		opts: opts,
		c: &fasthttp.Client{
			Name:            "restinvoker",
			MaxConnsPerHost: poolSize,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
		},
	}

	if opts.TLSConfig != nil {
		rc.c.TLSConfig = opts.TLSConfig
	}

	return rc
}

//Init is a method
func (c *Client) Init(opts ...clientOption.Option) error {
	for _, o := range opts {
		o(&c.opts)
	}

	return nil
}

//NewRequest do not use for rest client.
func (c *Client) NewRequest(service, schemaID, operationID string, arg interface{}, reqOpts ...clientOption.RequestOption) *client.Request {
	var opts clientOption.RequestOptions

	for _, o := range reqOpts {
		o(&opts)
	}

	i := &client.Request{
		MicroServiceName: service,
		Struct:           schemaID,
		Method:           operationID,
		Arg:              arg,
	}
	return i
}

// If a request fails, we generate an error.
func (c *Client) failure2Error(e error, r *Response) error {
	if e != nil {
		return e
	}

	if r == nil {
		return nil
	}

	codeStr := strconv.Itoa(r.GetStatusCode())
	// The Failure map defines whether or not a request fail.
	if c.opts.Failure["http_"+codeStr] {
		return fmt.Errorf("Get error status code: %d from http response: %s", r.GetStatusCode(), string(r.ReadBody()))
	}

	return nil
}

//Call is a method which uses client struct object
func (c *Client) Call(ctx context.Context, addr string, req *client.Request, rsp interface{}, opts ...clientOption.CallOption) error {
	var opt clientOption.CallOptions

	for _, o := range opts {
		o(&opt)
	}

	reqSend, ok := req.Arg.(*Request)
	if !ok {
		return errors.New("Rest consumer call arg is not *rest.Request type")
	}

	resp, ok := rsp.(*Response)
	if !ok {
		return errors.New("Rest consumer response arg is not *rest.Response type")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	urlPath := ""
	if c.opts.TLSConfig != nil {
		urlPath = fmt.Sprintf("https://%s%s", addr, opt.UrlPath)
	} else {
		urlPath = fmt.Sprintf("http://%s%s", addr, opt.UrlPath)
	}

	reqSend.SetURI(urlPath)

	//increase the max connection per host to prevent error "no free connection available" error while sending more requests.
	c.c.MaxConnsPerHost = 512 * 20

	errChan := make(chan error, 1)
	go func() { errChan <- c.Do(reqSend, resp) }()

	var err error
	select {
	case <-ctx.Done():
		err = errors.New("Request Cancelled")
	case err = <-errChan:
	}
	return c.failure2Error(err, resp)
}
func (c *Client) String() string {
	return "rest_client"
}

//Options is a method which used client struct object
func (c *Client) Options() clientOption.Options {
	return c.opts
}
