package coderd

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/xerrors"
	"tailscale.com/derp"
	"tailscale.com/tailcfg"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/tracing"
	"github.com/coder/coder/v2/coderd/workspaceapps"
	"github.com/coder/coder/v2/coderd/workspaceapps/appurl"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/site"
	"github.com/coder/coder/v2/tailnet"
	"github.com/coder/coder/v2/tailnet/proto"
)

var tailnetTransport *http.Transport

func init() {
	tp, valid := http.DefaultTransport.(*http.Transport)
	if !valid {
		panic("dev error: default transport is the wrong type")
	}
	tailnetTransport = tp.Clone()
	// We do not want to respect the proxy settings from the environment, since
	// all network traffic happens over wireguard.
	tailnetTransport.Proxy = nil
}

var _ workspaceapps.AgentProvider = (*ServerTailnet)(nil)

// NewServerTailnet creates a new tailnet intended for use by coderd.
func NewServerTailnet(
	ctx context.Context,
	logger slog.Logger,
	derpServer *derp.Server,
	dialer tailnet.ControlProtocolDialer,
	derpForceWebSockets bool,
	blockEndpoints bool,
	traceProvider trace.TracerProvider,
	agentIdleTimeout time.Duration,
) (*ServerTailnet, error) {
	logger = logger.Named("servertailnet")
	conn, err := tailnet.NewConn(&tailnet.Options{
		Addresses:           []netip.Prefix{tailnet.TailscaleServicePrefix.RandomPrefix()},
		DERPForceWebSockets: derpForceWebSockets,
		Logger:              logger,
		BlockEndpoints:      blockEndpoints,
	})
	if err != nil {
		return nil, xerrors.Errorf("create tailnet conn: %w", err)
	}
	serverCtx, cancel := context.WithCancel(ctx)

	// This is set to allow local DERP traffic to be proxied through memory
	// instead of needing to hit the external access URL. Don't use the ctx
	// given in this callback, it's only valid while connecting.
	if derpServer != nil {
		conn.SetDERPRegionDialer(func(_ context.Context, region *tailcfg.DERPRegion) net.Conn {
			// Don't set up the embedded relay if we're shutting down
			if !region.EmbeddedRelay || ctx.Err() != nil {
				return nil
			}
			logger.Debug(ctx, "connecting to embedded DERP via in-memory pipe")
			left, right := net.Pipe()
			go func() {
				defer left.Close()
				defer right.Close()
				brw := bufio.NewReadWriter(bufio.NewReader(right), bufio.NewWriter(right))
				derpServer.Accept(ctx, right, brw, "internal")
			}()
			return left
		})
	}

	tracer := traceProvider.Tracer(tracing.TracerName)
	if agentIdleTimeout <= 0 {
		agentIdleTimeout = codersdk.DefaultServerTailnetAgentIdleTimeout
	}

	controller := tailnet.NewController(logger, dialer)
	// it's important to set the DERPRegionDialer above _before_ we set the DERP map so that if
	// there is an embedded relay, we use the local in-memory dialer.
	controller.DERPCtrl = tailnet.NewBasicDERPController(logger, nil, conn)
	coordCtrl := NewMultiAgentController(serverCtx, logger, tracer, conn, agentIdleTimeout)
	controller.CoordCtrl = coordCtrl
	// TODO: support controller.TelemetryCtrl

	tn := &ServerTailnet{
		ctx:         serverCtx,
		cancel:      cancel,
		logger:      logger,
		tracer:      tracer,
		conn:        conn,
		coordinatee: conn,
		controller:  controller,
		coordCtrl:   coordCtrl,
		transport:   tailnetTransport.Clone(),
		connsPerAgent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "coder",
			Subsystem: "servertailnet",
			Name:      "open_connections",
			Help:      "Total number of TCP connections currently open to workspace agents.",
		}, []string{"network"}),
		totalConns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "coder",
			Subsystem: "servertailnet",
			Name:      "connections_total",
			Help:      "Total number of TCP connections made to workspace agents.",
		}, []string{"network"}),
	}
	tn.transport.DialContext = tn.dialContext
	// These options are mostly just picked at random, and they can likely be
	// fine-tuned further. Generally, users are running applications in dev mode
	// which can generate hundreds of requests per page load, so we increased
	// MaxIdleConnsPerHost from 2 to 6 and removed the limit of total idle
	// conns.
	tn.transport.MaxIdleConnsPerHost = 6
	tn.transport.MaxIdleConns = 0
	tn.transport.IdleConnTimeout = 10 * time.Minute
	// We intentionally don't verify the certificate chain here.
	// The connection to the workspace is already established and most
	// apps are already going to be accessed over plain HTTP, this config
	// simply allows apps being run over HTTPS to be accessed without error --
	// many of which may be using self-signed certs.
	tn.transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		//nolint:gosec
		InsecureSkipVerify: true,
	}

	tn.controller.Run(tn.ctx)
	return tn, nil
}

// Conn is used to access the underlying tailnet conn of the ServerTailnet. It
// should only be used for read-only purposes.
func (s *ServerTailnet) Conn() *tailnet.Conn {
	return s.conn
}

func (s *ServerTailnet) Describe(descs chan<- *prometheus.Desc) {
	s.connsPerAgent.Describe(descs)
	s.totalConns.Describe(descs)
}

func (s *ServerTailnet) Collect(metrics chan<- prometheus.Metric) {
	s.connsPerAgent.Collect(metrics)
	s.totalConns.Collect(metrics)
}

func (s *ServerTailnet) setMCPTraceRecorder(recorder *mcpTraceRecorder) {
	if s == nil || s.coordCtrl == nil {
		return
	}
	s.coordCtrl.setMCPTraceRecorder(recorder)
}

func (s *ServerTailnet) agentConnectionPath(agentID uuid.UUID) string {
	if s == nil || s.conn == nil {
		return "unknown"
	}
	status := s.conn.Status()
	if status == nil {
		return "unknown"
	}
	target := tailnet.TailscaleServicePrefix.AddrFromUUID(agentID)
	for _, peer := range status.Peer {
		if peer == nil {
			continue
		}
		matched := false
		for _, addr := range peer.TailscaleIPs {
			if addr == target {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if peer.CurAddr != "" {
			return "p2p"
		}
		if peer.Relay != "" {
			return "derp"
		}
		return "unknown"
	}
	return "unknown"
}

type ServerTailnet struct {
	ctx    context.Context
	cancel func()

	logger slog.Logger
	tracer trace.Tracer

	// in prod, these are the same, but coordinatee is a subset of Conn's
	// methods which makes some tests easier.
	conn        *tailnet.Conn
	coordinatee tailnet.Coordinatee

	controller *tailnet.Controller
	coordCtrl  *MultiAgentController

	transport *http.Transport

	connsPerAgent *prometheus.GaugeVec
	totalConns    *prometheus.CounterVec
}

func (s *ServerTailnet) ReverseProxy(targetURL, dashboardURL *url.URL, agentID uuid.UUID, app appurl.ApplicationURL, wildcardHostname string) *httputil.ReverseProxy {
	// Rewrite the targetURL's Host to point to the agent's IP. This is
	// necessary because due to TCP connection caching, each agent needs to be
	// addressed invidivually. Otherwise, all connections get dialed as
	// "localhost:port", causing connections to be shared across agents.
	tgt := *targetURL
	_, port, _ := net.SplitHostPort(tgt.Host)
	tgt.Host = net.JoinHostPort(tailnet.TailscaleServicePrefix.AddrFromUUID(agentID).String(), port)

	proxy := httputil.NewSingleHostReverseProxy(&tgt)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, theErr error) {
		var (
			desc           = "Failed to proxy request to application: " + theErr.Error()
			additionalInfo = ""
			actions        = []site.Action{}
		)

		var tlsError tls.RecordHeaderError
		if (errors.As(theErr, &tlsError) && tlsError.Msg == "first record does not look like a TLS handshake") ||
			errors.Is(theErr, http.ErrSchemeMismatch) {
			// If the error is due to an HTTP/HTTPS mismatch, we can provide a
			// more helpful error message with redirect buttons.
			switchURL := url.URL{
				Scheme: dashboardURL.Scheme,
			}
			_, protocol, isPort := app.PortInfo()
			if isPort {
				targetProtocol := "https"
				if protocol == "https" {
					targetProtocol = "http"
				}
				app = app.ChangePortProtocol(targetProtocol)

				switchURL.Host = fmt.Sprintf("%s%s", app.String(), strings.TrimPrefix(wildcardHostname, "*"))
				actions = append(actions, site.Action{
					URL:  switchURL.String(),
					Text: fmt.Sprintf("Switch to %s", strings.ToUpper(targetProtocol)),
				})
				additionalInfo += fmt.Sprintf("This error seems to be due to an app protocol mismatch, try switching to %s.", strings.ToUpper(targetProtocol))
			}
		}

		site.RenderStaticErrorPage(w, r, site.ErrorPageData{
			Status:      http.StatusBadGateway,
			Title:       "Bad Gateway",
			Description: desc,
			Actions: append(actions, []site.Action{
				{
					Text: "Retry",
				},
				{
					URL:  dashboardURL.String(),
					Text: "Back to site",
				},
			}...),
			AdditionalInfo: additionalInfo,
		})
	}
	proxy.Director = s.director(agentID, proxy.Director)
	proxy.Transport = s.transport

	return proxy
}

type agentIDKey struct{}

// director makes sure agentIDKey is set on the context in the reverse proxy.
// This allows the transport to correctly identify which agent to dial to.
func (*ServerTailnet) director(agentID uuid.UUID, prev func(req *http.Request)) func(req *http.Request) {
	return func(req *http.Request) {
		ctx := context.WithValue(req.Context(), agentIDKey{}, agentID)
		*req = *req.WithContext(ctx)
		prev(req)
	}
}

func (s *ServerTailnet) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	agentID, ok := ctx.Value(agentIDKey{}).(uuid.UUID)
	if !ok {
		return nil, xerrors.Errorf("no agent id attached")
	}

	nc, err := s.DialAgentNetConn(ctx, agentID, network, addr)
	if err != nil {
		return nil, err
	}

	s.connsPerAgent.WithLabelValues("tcp").Inc()
	s.totalConns.WithLabelValues("tcp").Inc()
	return &instrumentedConn{
		Conn:          nc,
		agentID:       agentID,
		connsPerAgent: s.connsPerAgent,
	}, nil
}

func (s *ServerTailnet) AgentConn(ctx context.Context, agentID uuid.UUID) (workspacesdk.AgentConn, func(), error) {
	var (
		conn workspacesdk.AgentConn
		ret  func()
	)

	s.logger.Debug(ctx, "acquiring agent", slog.F("agent_id", agentID))
	s.coordCtrl.traceAgentEvent(ctx, agentID, "agent_conn_acquire_started", nil)
	err := s.coordCtrl.ensureAgent(ctx, agentID)
	if err != nil {
		s.coordCtrl.traceAgentEvent(ctx, agentID, "agent_conn_acquire_failed", map[string]any{
			"stage": "ensure_agent",
			"error": err.Error(),
		})
		return nil, nil, xerrors.Errorf("ensure agent: %w", err)
	}
	ret = s.coordCtrl.acquireTicket(ctx, agentID)

	conn = workspacesdk.NewAgentConn(s.conn, workspacesdk.AgentConnOptions{
		AgentID:   agentID,
		CloseFunc: func() error { return workspacesdk.ErrSkipClose },
		Logger:    s.logger,
		Trace: func(_ context.Context, event string, details map[string]any) {
			// The AgentConn wrapper is created per acquisition. Capture the
			// acquisition context here so lower-level HTTP transports cannot lose
			// MCP workspace correlation when net/http derives its own dial context.
			s.coordCtrl.traceAgentEvent(ctx, agentID, event, details)
		},
	})

	// Since we now have an open conn, be careful to close it if we error
	// without returning it to the user.

	waitStarted := time.Now()
	s.coordCtrl.traceAgentEvent(ctx, agentID, "await_reachable_started", nil)
	reachable := conn.AwaitReachable(ctx)
	details := map[string]any{
		"duration_ms": time.Since(waitStarted).Milliseconds(),
		"reachable":   reachable,
	}
	if ctx.Err() != nil {
		details["context_error"] = ctx.Err().Error()
	}
	s.coordCtrl.traceAgentEvent(ctx, agentID, "await_reachable_finished", details)
	if !reachable {
		ret()
		s.coordCtrl.traceAgentEvent(ctx, agentID, "agent_conn_acquire_failed", map[string]any{
			"stage": "await_reachable",
		})
		return nil, nil, xerrors.New("agent is unreachable")
	}

	s.coordCtrl.traceAgentEvent(ctx, agentID, "agent_conn_acquired", map[string]any{
		"path": s.agentConnectionPath(agentID),
	})
	return conn, ret, nil
}

func (s *ServerTailnet) DialAgentNetConn(ctx context.Context, agentID uuid.UUID, network, addr string) (net.Conn, error) {
	conn, release, err := s.AgentConn(ctx, agentID)
	if err != nil {
		return nil, xerrors.Errorf("acquire agent conn: %w", err)
	}

	// Since we now have an open conn, be careful to close it if we error
	// without returning it to the user.

	nc, err := conn.DialContext(ctx, network, addr)
	if err != nil {
		release()
		return nil, xerrors.Errorf("dial context: %w", err)
	}

	return &netConnCloser{Conn: nc, close: func() {
		release()
	}}, err
}

func (s *ServerTailnet) ServeHTTPDebug(w http.ResponseWriter, r *http.Request) {
	s.conn.MagicsockServeHTTPDebug(w, r)
}

type netConnCloser struct {
	net.Conn
	close func()
}

func (c *netConnCloser) Close() error {
	c.close()
	return c.Conn.Close()
}

func (s *ServerTailnet) Close() error {
	s.logger.Info(s.ctx, "closing server tailnet")
	defer s.logger.Debug(s.ctx, "server tailnet close complete")
	s.cancel()
	_ = s.conn.Close()
	s.transport.CloseIdleConnections()
	s.coordCtrl.Close()
	<-s.controller.Closed()
	return nil
}

type instrumentedConn struct {
	net.Conn

	agentID       uuid.UUID
	closeOnce     sync.Once
	connsPerAgent *prometheus.GaugeVec
}

func (c *instrumentedConn) Close() error {
	c.closeOnce.Do(func() {
		c.connsPerAgent.WithLabelValues("tcp").Dec()
	})
	return c.Conn.Close()
}

// MultiAgentController is a tailnet.CoordinationController for connecting to multiple workspace
// agents. It keeps track of connection times to the agents, and removes them on a timer if they
// have no active connections and haven't been used in a while.
type multiAgentConnectionState struct {
	lastConnection time.Time
	traceID        uuid.UUID
	workspaceID    uuid.UUID
}

type tracingCloserWaiter struct {
	inner tailnet.CloserWaiter
	wait  chan error
}

func newTracingCloserWaiter(inner tailnet.CloserWaiter, onDone func(error)) tailnet.CloserWaiter {
	traced := &tracingCloserWaiter{
		inner: inner,
		wait:  make(chan error, 1),
	}
	go func() {
		err, ok := <-inner.Wait()
		if !ok {
			err = nil
		}
		if onDone != nil {
			onDone(err)
		}
		traced.wait <- err
		close(traced.wait)
	}()
	return traced
}

func (t *tracingCloserWaiter) Close(ctx context.Context) error {
	return t.inner.Close(ctx)
}

func (t *tracingCloserWaiter) Wait() <-chan error {
	return t.wait
}

type MultiAgentController struct {
	*tailnet.BasicCoordinationController

	logger slog.Logger
	tracer trace.Tracer

	traceRecorder atomic.Pointer[mcpTraceRecorder]
	idleTimeout   time.Duration

	mu sync.Mutex
	// connectionTimes is a map of agents the server wants to keep a connection to.
	// It contains the last use time plus the latest MCP trace correlation, when available.
	connectionTimes map[uuid.UUID]multiAgentConnectionState
	// tickets is a map of destinations to a set of connection tickets, representing open
	// connections to the destination.
	tickets      map[uuid.UUID]map[uuid.UUID]struct{}
	coordination *tailnet.BasicCoordination

	cancel              context.CancelFunc
	expireOldAgentsDone chan struct{}
}

func (m *MultiAgentController) setMCPTraceRecorder(recorder *mcpTraceRecorder) {
	m.traceRecorder.Store(recorder)
}

func (m *MultiAgentController) traceAgentEvent(ctx context.Context, agentID uuid.UUID, event string, details any) {
	recorder := m.traceRecorder.Load()
	if recorder == nil {
		return
	}
	recorder.AgentEvent(ctx, agentID, event, details)
}

func (m *MultiAgentController) traceAgentEventForState(state multiAgentConnectionState, agentID uuid.UUID, event string, details any) {
	recorder := m.traceRecorder.Load()
	if recorder == nil || state.traceID == uuid.Nil {
		return
	}
	recorder.AgentEventFor(state.traceID, state.workspaceID, agentID, event, details)
}

func (m *MultiAgentController) traceCoordinationDisconnected(err error) {
	reason := "closed"
	details := map[string]any{}
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
		reason = "eof"
	case errors.Is(err, context.Canceled):
		reason = "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline_exceeded"
	default:
		reason = "error"
		details["error"] = err.Error()
	}
	details["reason"] = reason

	m.mu.Lock()
	states := make(map[uuid.UUID]multiAgentConnectionState, len(m.connectionTimes))
	for agentID, state := range m.connectionTimes {
		states[agentID] = state
	}
	m.mu.Unlock()

	for agentID, state := range states {
		m.traceAgentEventForState(state, agentID, "coordination_disconnected", details)
	}
}

func (m *MultiAgentController) New(client tailnet.CoordinatorClient) tailnet.CloserWaiter {
	b := m.BasicCoordinationController.NewCoordination(client)
	// Resync all destinations after a coordination reconnect.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordination = b
	for agentID, state := range m.connectionTimes {
		err := b.SendRequest(&proto.CoordinateRequest{
			AddTunnel: &proto.CoordinateRequest_Tunnel{Id: agentID[:]},
		})
		if err != nil {
			m.traceAgentEventForState(state, agentID, "coordination_resubscribe_failed", map[string]any{
				"error": err.Error(),
			})
			m.logger.Error(context.Background(), "failed to re-add tunnel", slog.F("agent_id", agentID),
				slog.Error(err))
			b.SendErr(err)
			_ = client.Close()
			m.coordination = nil
			break
		}
		m.traceAgentEventForState(state, agentID, "coordination_resubscribed", map[string]any{
			"ticket_count": len(m.tickets[agentID]),
		})
	}
	return newTracingCloserWaiter(b, m.traceCoordinationDisconnected)
}

func (m *MultiAgentController) ensureAgent(ctx context.Context, agentID uuid.UUID) error {
	now := time.Now()
	traceID, workspaceID := mcpAgentTraceMetadataFromContext(ctx)

	m.mu.Lock()
	state, exists := m.connectionTimes[agentID]
	previousLastConnection := state.lastConnection
	state.traceID = traceID
	state.workspaceID = workspaceID
	ticketCountBefore := len(m.tickets[agentID])
	addedTunnel := false
	deferredTunnel := false

	// If we don't have the agent, subscribe.
	if !exists {
		m.logger.Debug(ctx, "subscribing to agent", slog.F("agent_id", agentID))
		if m.coordination != nil {
			err := m.coordination.SendRequest(&proto.CoordinateRequest{
				AddTunnel: &proto.CoordinateRequest_Tunnel{Id: agentID[:]},
			})
			if err != nil {
				err = xerrors.Errorf("subscribe agent: %w", err)
				m.coordination.SendErr(err)
				_ = m.coordination.CloseClient()
				m.coordination = nil
				m.mu.Unlock()
				m.traceAgentEventForState(state, agentID, "add_tunnel_failed", map[string]any{
					"error": err.Error(),
				})
				return err
			}
			addedTunnel = true
		} else {
			deferredTunnel = true
		}
		m.tickets[agentID] = map[uuid.UUID]struct{}{}
	}
	state.lastConnection = now
	m.connectionTimes[agentID] = state
	m.mu.Unlock()

	details := map[string]any{
		"already_subscribed":  exists,
		"ticket_count_before": ticketCountBefore,
	}
	if !previousLastConnection.IsZero() {
		details["previous_last_connection_age_ms"] = now.Sub(previousLastConnection).Milliseconds()
	}
	m.traceAgentEventForState(state, agentID, "ensure_agent", details)
	if addedTunnel {
		m.traceAgentEventForState(state, agentID, "add_tunnel", map[string]any{"reason": "first_use"})
	}
	if deferredTunnel {
		m.traceAgentEventForState(state, agentID, "add_tunnel_deferred", map[string]any{"reason": "coordination_unavailable"})
	}
	return nil
}

func (m *MultiAgentController) acquireTicket(ctx context.Context, agentID uuid.UUID) (release func()) {
	id := uuid.New()
	traceID, workspaceID := mcpAgentTraceMetadataFromContext(ctx)

	m.mu.Lock()
	state := m.connectionTimes[agentID]
	state.traceID = traceID
	state.workspaceID = workspaceID
	m.connectionTimes[agentID] = state
	m.tickets[agentID][id] = struct{}{}
	ticketCount := len(m.tickets[agentID])
	m.mu.Unlock()

	m.traceAgentEventForState(state, agentID, "ticket_acquired", map[string]any{
		"ticket_count": ticketCount,
	})

	return func() {
		m.mu.Lock()
		delete(m.tickets[agentID], id)
		remaining := len(m.tickets[agentID])
		m.mu.Unlock()
		m.traceAgentEventForState(state, agentID, "ticket_released", map[string]any{
			"ticket_count": remaining,
		})
	}
}

func (m *MultiAgentController) expireOldAgents(ctx context.Context) {
	defer close(m.expireOldAgentsDone)
	defer m.logger.Debug(context.Background(), "stopped expiring old agents")
	const tick = 5 * time.Minute

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.doExpireOldAgents(ctx, m.idleTimeout)
	}
}

func (m *MultiAgentController) doExpireOldAgents(ctx context.Context, cutoff time.Duration) {
	ctx, span := m.tracer.Start(ctx, tracing.FuncName())
	defer span.End()

	start := time.Now()
	deletedCount := 0

	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger.Debug(ctx, "pruning inactive agents", slog.F("agent_count", len(m.connectionTimes)), slog.F("cutoff", cutoff))
	for agentID, state := range m.connectionTimes {
		idleFor := time.Since(state.lastConnection)
		ticketCount := len(m.tickets[agentID])
		// If no one has connected since the cutoff and there are no active
		// connections, remove the agent.
		if idleFor > cutoff && ticketCount == 0 {
			m.traceAgentEventForState(state, agentID, "idle_timeout_expired", map[string]any{
				"idle_ms":      idleFor.Milliseconds(),
				"cutoff_ms":    cutoff.Milliseconds(),
				"ticket_count": ticketCount,
			})
			if m.coordination != nil {
				err := m.coordination.SendRequest(&proto.CoordinateRequest{
					RemoveTunnel: &proto.CoordinateRequest_Tunnel{Id: agentID[:]},
				})
				if err != nil {
					m.traceAgentEventForState(state, agentID, "remove_tunnel_failed", map[string]any{
						"reason": "idle_timeout",
						"error":  err.Error(),
					})
					m.logger.Debug(ctx, "unsubscribe expired agent", slog.Error(err), slog.F("agent_id", agentID))
					m.coordination.SendErr(xerrors.Errorf("unsubscribe expired agent: %w", err))
					// Close the client because we do not want to do a graceful disconnect by
					// closing the coordination.
					_ = m.coordination.CloseClient()
					m.coordination = nil
					// Continue deleting inactive agents: there is no point in re-establishing
					// tunnels to expired agents when we eventually reconnect.
				} else {
					m.traceAgentEventForState(state, agentID, "remove_tunnel", map[string]any{
						"reason": "idle_timeout",
					})
				}
			} else {
				m.traceAgentEventForState(state, agentID, "remove_tunnel_deferred", map[string]any{
					"reason": "coordination_unavailable",
				})
			}
			deletedCount++
			delete(m.connectionTimes, agentID)
		}
	}
	m.logger.Debug(ctx, "pruned inactive agents",
		slog.F("deleted", deletedCount),
		slog.F("took", time.Since(start)),
	)
}

func (m *MultiAgentController) Close() {
	m.cancel()
	<-m.expireOldAgentsDone
}

func NewMultiAgentController(ctx context.Context, logger slog.Logger, tracer trace.Tracer, coordinatee tailnet.Coordinatee, idleTimeout time.Duration) *MultiAgentController {
	if idleTimeout <= 0 {
		idleTimeout = codersdk.DefaultServerTailnetAgentIdleTimeout
	}
	m := &MultiAgentController{
		BasicCoordinationController: &tailnet.BasicCoordinationController{
			Logger:      logger,
			Coordinatee: coordinatee,
			SendAcks:    false, // we are a client, connecting to multiple agents
			Initiator:   codersdk.DisconnectInitiatorServer,
			Direction:   codersdk.ConnectionDirectionServerToAgent,
		},
		logger:              logger,
		tracer:              tracer,
		idleTimeout:         idleTimeout,
		connectionTimes:     make(map[uuid.UUID]multiAgentConnectionState),
		tickets:             make(map[uuid.UUID]map[uuid.UUID]struct{}),
		expireOldAgentsDone: make(chan struct{}),
	}
	ctx, m.cancel = context.WithCancel(ctx)
	go m.expireOldAgents(ctx)
	return m
}

type Pinger interface {
	Ping(context.Context) (time.Duration, error)
}

// InmemTailnetDialer is a tailnet.ControlProtocolDialer that connects to a Coordinator and DERPMap
// service running in the same memory space.
type InmemTailnetDialer struct {
	CoordPtr *atomic.Pointer[tailnet.Coordinator]
	DERPFn   func() *tailcfg.DERPMap
	Logger   slog.Logger
	ClientID uuid.UUID
	// DatabaseHealthCheck is used to validate that the store is reachable.
	DatabaseHealthCheck Pinger
}

func (a *InmemTailnetDialer) Dial(ctx context.Context, _ tailnet.ResumeTokenController) (tailnet.ControlProtocolClients, error) {
	if a.DatabaseHealthCheck != nil {
		if _, err := a.DatabaseHealthCheck.Ping(ctx); err != nil {
			return tailnet.ControlProtocolClients{}, xerrors.Errorf("%w: %v", codersdk.ErrDatabaseNotReachable, err)
		}
	}

	coord := a.CoordPtr.Load()
	if coord == nil {
		return tailnet.ControlProtocolClients{}, xerrors.Errorf("tailnet coordinator not initialized")
	}
	coordClient := tailnet.NewInMemoryCoordinatorClient(
		a.Logger, a.ClientID, tailnet.SingleTailnetCoordinateeAuth{}, *coord)
	derpClient := newPollingDERPClient(a.DERPFn, a.Logger)
	return tailnet.ControlProtocolClients{
		Closer:      closeAll{coord: coordClient, derp: derpClient},
		Coordinator: coordClient,
		DERP:        derpClient,
	}, nil
}

func newPollingDERPClient(derpFn func() *tailcfg.DERPMap, logger slog.Logger) tailnet.DERPClient {
	ctx, cancel := context.WithCancel(context.Background())
	a := &pollingDERPClient{
		fn:       derpFn,
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger,
		ch:       make(chan *tailcfg.DERPMap),
		loopDone: make(chan struct{}),
	}
	go a.pollDERP()
	return a
}

// pollingDERPClient is a DERP client that just calls a function on a polling
// interval
type pollingDERPClient struct {
	fn          func() *tailcfg.DERPMap
	logger      slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	loopDone    chan struct{}
	lastDERPMap *tailcfg.DERPMap
	ch          chan *tailcfg.DERPMap
}

// Close the DERP client
func (a *pollingDERPClient) Close() error {
	a.cancel()
	<-a.loopDone
	return nil
}

func (a *pollingDERPClient) Recv() (*tailcfg.DERPMap, error) {
	select {
	case <-a.ctx.Done():
		return nil, a.ctx.Err()
	case dm := <-a.ch:
		return dm, nil
	}
}

func (a *pollingDERPClient) pollDERP() {
	defer close(a.loopDone)
	defer a.logger.Debug(a.ctx, "polling DERPMap exited")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
		}

		newDerpMap := a.fn()
		if !tailnet.CompareDERPMaps(a.lastDERPMap, newDerpMap) {
			select {
			case <-a.ctx.Done():
				return
			case a.ch <- newDerpMap:
			}
		}
	}
}

type closeAll struct {
	coord tailnet.CoordinatorClient
	derp  tailnet.DERPClient
}

func (c closeAll) Close() error {
	cErr := c.coord.Close()
	dErr := c.derp.Close()
	if cErr != nil {
		return cErr
	}
	return dErr
}
