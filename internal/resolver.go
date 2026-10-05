package internal

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"

	"github.com/Muxcore-Media/core/sdk/go/client"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
)

// LibraryLookup resolves library ids and genres from media-movies / media-tvshows.
type LibraryLookup interface {
	LookupMovieByTMDB(ctx context.Context, tmdbID int32) (movieID string, genres []string, ok bool)
	LookupTVByTMDB(ctx context.Context, tmdbID int32) (seriesID string, genres []string, ok bool)
	GetMovieGenres(ctx context.Context, movieID string) ([]string, error)
	GetTVGenres(ctx context.Context, seriesID string) ([]string, error)
}

type noopLookup struct{}

func (noopLookup) LookupMovieByTMDB(context.Context, int32) (string, []string, bool) {
	return "", nil, false
}
func (noopLookup) LookupTVByTMDB(context.Context, int32) (string, []string, bool) {
	return "", nil, false
}
func (noopLookup) GetMovieGenres(context.Context, string) ([]string, error) { return nil, nil }
func (noopLookup) GetTVGenres(context.Context, string) ([]string, error)    { return nil, nil }

// MockLookup is an in-memory LibraryLookup for tests.
type MockLookup struct {
	MoviesByTMDB map[int32]struct {
		ID     string
		Genres []string
	}
	TVByTMDB map[int32]struct {
		ID     string
		Genres []string
	}
	MovieGenres map[string][]string
	TVGenres    map[string][]string
}

func (m *MockLookup) LookupMovieByTMDB(_ context.Context, tmdbID int32) (string, []string, bool) {
	if m == nil || m.MoviesByTMDB == nil {
		return "", nil, false
	}
	v, ok := m.MoviesByTMDB[tmdbID]
	return v.ID, append([]string(nil), v.Genres...), ok
}

func (m *MockLookup) LookupTVByTMDB(_ context.Context, tmdbID int32) (string, []string, bool) {
	if m == nil || m.TVByTMDB == nil {
		return "", nil, false
	}
	v, ok := m.TVByTMDB[tmdbID]
	return v.ID, append([]string(nil), v.Genres...), ok
}

func (m *MockLookup) GetMovieGenres(_ context.Context, movieID string) ([]string, error) {
	if m == nil || m.MovieGenres == nil {
		return nil, nil
	}
	return append([]string(nil), m.MovieGenres[movieID]...), nil
}

func (m *MockLookup) GetTVGenres(_ context.Context, seriesID string) ([]string, error) {
	if m == nil || m.TVGenres == nil {
		return nil, nil
	}
	return append([]string(nil), m.TVGenres[seriesID]...), nil
}

type meshLookup struct { //nolint:govet // fieldalignment: lazy dial fields grouped for readability
	mc *client.Client

	mu         sync.Mutex
	moviesConn *grpc.ClientConn
	movies     mgmntv1.MovieManagementServiceClient
	tvConn     *grpc.ClientConn
	tv         tvmgmtv1.TvManagementServiceClient
}

func newMeshLookup(mc *client.Client) LibraryLookup {
	if mc == nil {
		return noopLookup{}
	}
	return &meshLookup{mc: mc}
}

func (m *meshLookup) dialMovies(ctx context.Context) (mgmntv1.MovieManagementServiceClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.movies != nil {
		return m.movies, nil
	}
	info, err := m.mc.Discovery.FindByCapability(ctx, "media.library.movies")
	if err != nil || len(info) == 0 {
		return nil, fmt.Errorf("movies module: %w", err)
	}
	conn, err := meshtls.Dial(info[0].GetHttpAddr())
	if err != nil {
		return nil, err
	}
	m.moviesConn = conn
	m.movies = mgmntv1.NewMovieManagementServiceClient(conn)
	return m.movies, nil
}

func (m *meshLookup) dialTV(ctx context.Context) (tvmgmtv1.TvManagementServiceClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tv != nil {
		return m.tv, nil
	}
	info, err := m.mc.Discovery.FindByCapability(ctx, "media.library.tv")
	if err != nil || len(info) == 0 {
		return nil, fmt.Errorf("tv module: %w", err)
	}
	conn, err := meshtls.Dial(info[0].GetHttpAddr())
	if err != nil {
		return nil, err
	}
	m.tvConn = conn
	m.tv = tvmgmtv1.NewTvManagementServiceClient(conn)
	return m.tv, nil
}

func (m *meshLookup) LookupMovieByTMDB(ctx context.Context, tmdbID int32) (string, []string, bool) {
	cli, err := m.dialMovies(ctx)
	if err != nil {
		return "", nil, false
	}
	for page := int32(1); ; page++ {
		resp, err := cli.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: page, PageSize: 100})
		if err != nil || len(resp.GetMovies()) == 0 {
			return "", nil, false
		}
		for _, mv := range resp.GetMovies() {
			if mv.GetTmdbId() == tmdbID {
				return mv.GetId(), append([]string(nil), mv.GetGenres()...), true
			}
		}
		pageSize := resp.GetPageSize()
		if int32(len(resp.GetMovies())) < pageSize || page*pageSize >= resp.GetTotal() { //nolint:gosec // bounded library page
			break
		}
	}
	return "", nil, false
}

func (m *meshLookup) LookupTVByTMDB(ctx context.Context, tmdbID int32) (string, []string, bool) {
	cli, err := m.dialTV(ctx)
	if err != nil {
		return "", nil, false
	}
	for page := int32(1); ; page++ {
		resp, err := cli.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{Page: page, PageSize: 100})
		if err != nil || len(resp.GetSeries()) == 0 {
			return "", nil, false
		}
		for _, s := range resp.GetSeries() {
			if s.GetTmdbId() == tmdbID {
				return s.GetId(), append([]string(nil), s.GetGenres()...), true
			}
		}
		pageSize := resp.GetPageSize()
		if int32(len(resp.GetSeries())) < pageSize || page*pageSize >= resp.GetTotal() { //nolint:gosec // bounded library page
			break
		}
	}
	return "", nil, false
}

func (m *meshLookup) GetMovieGenres(ctx context.Context, movieID string) ([]string, error) {
	cli, err := m.dialMovies(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := cli.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: movieID})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), resp.GetMovie().GetGenres()...), nil
}

func (m *meshLookup) GetTVGenres(ctx context.Context, seriesID string) ([]string, error) {
	cli, err := m.dialTV(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := cli.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: seriesID})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), resp.GetSeries().GetGenres()...), nil
}
