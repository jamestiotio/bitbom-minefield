package v1

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"
	service "github.com/bitbomdev/minefield/gen/api/v1"
	"github.com/bitbomdev/minefield/pkg/graph"
	"github.com/bitbomdev/minefield/pkg/tools/ingest"
	"github.com/goccy/go-json"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Service struct {
	storage     graph.Storage
	concurrency int32
}

func NodeToServiceNode(node *graph.Node) (*service.Node, error) {
	data, err := json.Marshal(node.Metadata)
	if err != nil {
		return nil, err
	}

	return &service.Node{
		Id:           node.ID,
		Name:         node.Name,
		Type:         node.Type,
		Metadata:     data,
		Dependencies: node.Children.ToArray(),
		Dependents:   node.Parents.ToArray(),
	}, nil
}

func NewService(storage graph.Storage, concurrency int32) *Service {
	return &Service{storage: storage, concurrency: concurrency}
}

type Query struct {
	Node   graph.Node
	Output []uint32
}

func (s *Service) GetNode(ctx context.Context, req *connect.Request[service.GetNodeRequest]) (*connect.Response[service.GetNodeResponse], error) {
	node, err := s.storage.GetNode(req.Msg.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to get node by id: %w", err)
	}
	serviceNode, err := NodeToServiceNode(node)
	if err != nil {
		return nil, fmt.Errorf("failed to convert node to service node: %w", err)
	}
	return connect.NewResponse(&service.GetNodeResponse{Node: serviceNode}), nil
}

func (s *Service) GetNodeByName(ctx context.Context, req *connect.Request[service.GetNodeByNameRequest]) (*connect.Response[service.GetNodeByNameResponse], error) {
	id, err := s.storage.NameToID(req.Msg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get node by name: %w", err)
	}
	node, err := s.storage.GetNode(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get node by id: %w", err)
	}
	serviceNode, err := NodeToServiceNode(node)
	if err != nil {
		return nil, fmt.Errorf("failed to convert node to service node: %w", err)
	}
	return connect.NewResponse(&service.GetNodeByNameResponse{Node: serviceNode}), nil
}

func (s *Service) GetNodesByGlob(ctx context.Context, req *connect.Request[service.GetNodesByGlobRequest]) (*connect.Response[service.GetNodesByGlobResponse], error) {
	nodes, err := s.storage.GetNodesByGlob(req.Msg.Pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by glob: %w", err)
	}
	serviceNodes := make([]*service.Node, 0, len(nodes))
	for _, node := range nodes {
		serviceNode, err := NodeToServiceNode(node)
		if err != nil {
			return nil, fmt.Errorf("failed to convert node to service node: %w", err)
		}
		serviceNodes = append(serviceNodes, serviceNode)
	}
	return connect.NewResponse(&service.GetNodesByGlobResponse{Nodes: serviceNodes}), nil
}

func (s *Service) AddNode(ctx context.Context, req *connect.Request[service.AddNodeRequest]) (*connect.Response[service.AddNodeResponse], error) {
	resultNode, err := graph.AddNode(s.storage, req.Msg.Node.Type, req.Msg.Node.Metadata, req.Msg.Node.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to add node: %w", err)
	}
	serviceNode, err := NodeToServiceNode(resultNode)
	if err != nil {
		return nil, fmt.Errorf("failed to convert node to service node: %w", err)
	}
	return connect.NewResponse(&service.AddNodeResponse{Node: serviceNode}), nil
}

func (s *Service) SetDependency(ctx context.Context, req *connect.Request[service.SetDependencyRequest]) (*connect.Response[emptypb.Empty], error) {
	fromNode, err := s.storage.GetNode(req.Msg.NodeId)
	if err != nil {
		return nil, err
	}
	toNode, err := s.storage.GetNode(req.Msg.DependencyID)
	if err != nil {
		return nil, err
	}
	err = fromNode.SetDependency(s.storage, toNode)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *Service) Cache(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
	err := graph.Cache(s.storage)
	if err != nil {
		return nil, fmt.Errorf("failed to cache: %w", err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *Service) Clear(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
	err := s.storage.RemoveAllCaches()
	if err != nil {
		return nil, fmt.Errorf("failed to clear: %w", err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *Service) CustomLeaderboard(ctx context.Context, req *connect.Request[service.CustomLeaderboardRequest]) (*connect.Response[service.CustomLeaderboardResponse], error) {
	uncachedNodes, err := s.storage.ToBeCached()
	if err != nil {
		return nil, fmt.Errorf("failed to get uncached nodes: %w", err)
	}
	if len(uncachedNodes) != 0 {
		return nil, fmt.Errorf("cannot use sorted leaderboards without caching")
	}

	keys, err := s.storage.GetAllKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to Query keys: %w", err)
	}

	nodes, err := s.storage.GetNodes(keys)
	if err != nil {
		return nil, fmt.Errorf("failed to batch Query nodes from keys: %w", err)
	}

	caches, err := s.storage.GetCaches(keys)
	if err != nil {
		return nil, fmt.Errorf("failed to batch Query caches from keys: %w", err)
	}

	cacheStack, err := s.storage.ToBeCached()
	if err != nil {
		return nil, err
	}

	h := &queryHeap{}
	heap.Init(h)

	// Use maxConcurrency in your parallel processing code
	semaphore := make(chan struct{}, s.concurrency)

	// Create channels for queries and errors
	queryChan := make(chan *Query, len(nodes))
	errChan := make(chan error, len(nodes))

	var wg sync.WaitGroup
	var atomicCounter int64
	for _, node := range nodes {
		if node.Name == "" {
			continue
		}

		wg.Add(1)
		semaphore <- struct{}{} // Acquire a token
		go func(node *graph.Node) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release the token

			execute, err := graph.ParseAndExecute(req.Msg.Script, s.storage, node.Name, nodes, caches, len(cacheStack) == 0)
			if err != nil {
				errChan <- err
				return
			}

			output := execute.ToArray()
			atomic.AddInt64(&atomicCounter, 1)
			queryChan <- &Query{Node: *node, Output: output}
		}(node)
	}
	// Close channels once all goroutines are done
	go func() {
		wg.Wait()
		close(queryChan)
		close(errChan)
		close(semaphore) // Close the semaphore channel
	}()

	// Check for errors
	select {
	case err := <-errChan:
		if err != nil {
			return nil, err
		}
	default:
	}
	for q := range queryChan {
		heap.Push(h, q)
	}

	queries := make([]*service.Query, h.Len())
	for i := len(queries) - 1; i >= 0; i-- {
		graphQuery := heap.Pop(h).(*Query)
		query, err := NodeToServiceNode(&graphQuery.Node)
		if err != nil {
			return nil, err
		}
		queries[i] = &service.Query{
			Node:   query,
			Output: graphQuery.Output,
		}
	}

	res := connect.NewResponse(&service.CustomLeaderboardResponse{
		Queries: queries,
	})
	res.Header().Set("Service-Version", "v1")
	return res, nil
}

func (s *Service) AllKeys(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[service.AllKeysResponse], error) {
	keys, err := s.storage.GetAllKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to get all keys: %w", err)
	}
	nodes, err := s.storage.GetNodes(keys)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by keys: %w", err)
	}

	resultNodes := make([]*service.Node, 0, len(nodes))
	for _, node := range nodes {
		query, err := NodeToServiceNode(node)
		if err != nil {
			return nil, fmt.Errorf("failed to convert node to service node: %w", err)	
		}
		resultNodes = append(resultNodes, query)
	}

	return connect.NewResponse(&service.AllKeysResponse{
		Nodes: resultNodes,
	}), nil
}

func (s *Service) Query(ctx context.Context, req *connect.Request[service.QueryRequest]) (*connect.Response[service.QueryResponse], error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	keys, err := s.storage.GetAllKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to get all keys: %w", err)
	}

	nodes, err := s.storage.GetNodes(keys)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by keys: %w", err)
	}

	caches, err := s.storage.GetCaches(keys)
	if err != nil {
		return nil, fmt.Errorf("failed to get caches by keys: %w", err)
	}
	cacheStack, err := s.storage.ToBeCached()
	if err != nil {
		return nil, fmt.Errorf("failed to get to be cached nodes: %w", err)
	}
	result, err := graph.ParseAndExecute(req.Msg.Script, s.storage, "", nodes, caches, len(cacheStack) == 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse and execute script: %w", err)
	}

	outputNodes, err := s.storage.GetNodes(result.ToArray())
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes by ids: %w", err)
	}

	resultNodes := make([]*service.Node, 0, len(outputNodes))
	for _, node := range outputNodes {
		query, err := NodeToServiceNode(node)
		if err != nil {
			return nil, fmt.Errorf("failed to convert node to service node: %w", err)
		}
		resultNodes = append(resultNodes, query)
	}

	res := connect.NewResponse(&service.QueryResponse{
		Nodes: resultNodes,
	})
	res.Header().Set("Service-Version", "v1")
	return res, nil
}

func (s *Service) Check(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[service.HealthCheckResponse], error) {
	return connect.NewResponse(&service.HealthCheckResponse{Status: "ok"}), nil
}

func (s *Service) IngestSBOM(ctx context.Context, req *connect.Request[service.IngestSBOMRequest]) (*connect.Response[emptypb.Empty], error) {
	err := ingest.SBOM(s.storage, req.Msg.Sbom)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest sbom: %w", err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *Service) IngestVulnerability(ctx context.Context, req *connect.Request[service.IngestVulnerabilityRequest]) (*connect.Response[emptypb.Empty], error) {
	err := ingest.Vulnerabilities(s.storage, req.Msg.Vulnerability)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest vulnerability: %w", err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *Service) IngestScorecard(ctx context.Context, req *connect.Request[service.IngestScorecardRequest]) (*connect.Response[emptypb.Empty], error) {
	err := ingest.Scorecards(s.storage, req.Msg.Scorecard)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest scorecard: %w", err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

type queryHeap []*Query

func (h queryHeap) Len() int { return len(h) }
func (h queryHeap) Less(i, j int) bool {
	return len(h[i].Output) < len(h[j].Output)
}
func (h queryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *queryHeap) Push(x interface{}) {
	*h = append(*h, x.(*Query))
}

func (h *queryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
