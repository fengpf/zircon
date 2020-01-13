package rpc

import (
	"context"
	"errors"
	"net/http"
	"zircon/apis"
	"zircon/rpc/twirp"
)

// Connects to an RPC handler for a MetadataCache on a certain address.
func UncachedSubscribeMetadataCache(address apis.ServerAddress, client *http.Client) (apis.MetadataCache, error) {
	saddr := "http://" + string(address)
	tserve := twirp.NewMetadataCacheProtobufClient(saddr, client)

	return &proxyTwirpAsMetadataCache{server: tserve}, nil
}

// Starts serving an RPC handler for a MetadataCache on a certain address. Runs forever.
func PublishMetadataCache(server apis.MetadataCache, address apis.ServerAddress) (func(kill bool) error, apis.ServerAddress, error) {
	tserve := twirp.NewMetadataCacheServer(&proxyMetadataCacheAsTwirp{server: server}, nil)
	return LaunchEmbeddedHTTP(tserve, address)
}

type proxyMetadataCacheAsTwirp struct {
	server apis.MetadataCache
}

func (p *proxyMetadataCacheAsTwirp) NewEntry(ctx context.Context, request *twirp.MetadataCache_NewEntry) (*twirp.MetadataCache_NewEntry_Result, error) {
	chunk, err := p.server.NewEntry()
	if err != nil {
		return nil, err
	}
	return &twirp.MetadataCache_NewEntry_Result{
		Chunk: uint64(chunk),
	}, nil
}

func (p *proxyMetadataCacheAsTwirp) ReadEntry(ctx context.Context, request *twirp.MetadataCache_ReadEntry) (*twirp.MetadataCache_ReadEntry_Result, error) {
	entry, owner, err := p.server.ReadEntry(apis.ChunkNum(request.Chunk))
	if err != nil {
		if owner == "" {
			return nil, err
		}
		return &twirp.MetadataCache_ReadEntry_Result{
			Owner:    string(owner),
			OwnerErr: err.Error(),
		}, nil
	}
	return &twirp.MetadataCache_ReadEntry_Result{
		Entry: &twirp.MetadataEntry{
			MostRecentVersion:   uint64(entry.MostRecentVersion),
			LastConsumedVersion: uint64(entry.LastConsumedVersion),
			ServerIDs:           IDArrayToIntArray(entry.Replicas),
		},
	}, nil
}

func (p *proxyMetadataCacheAsTwirp) UpdateEntry(ctx context.Context, request *twirp.MetadataCache_UpdateEntry) (*twirp.MetadataCache_UpdateEntry_Result, error) {
	owner, err := p.server.UpdateEntry(apis.ChunkNum(request.Chunk), apis.MetadataEntry{
		MostRecentVersion:   apis.Version(request.PreviousEntry.MostRecentVersion),
		LastConsumedVersion: apis.Version(request.PreviousEntry.LastConsumedVersion),
		Replicas:            IntArrayToIDArray(request.PreviousEntry.ServerIDs),
	}, apis.MetadataEntry{
		MostRecentVersion:   apis.Version(request.NewEntry.MostRecentVersion),
		LastConsumedVersion: apis.Version(request.NewEntry.LastConsumedVersion),
		Replicas:            IntArrayToIDArray(request.NewEntry.ServerIDs),
	})
	if owner != "" {
		return &twirp.MetadataCache_UpdateEntry_Result{
			Owner:    string(owner),
			OwnerErr: err.Error(),
		}, nil
	}
	return &twirp.MetadataCache_UpdateEntry_Result{
		Owner: string(owner),
	}, err
}

func (p *proxyMetadataCacheAsTwirp) DeleteEntry(ctx context.Context, request *twirp.MetadataCache_DeleteEntry) (*twirp.MetadataCache_DeleteEntry_Result, error) {
	owner, err := p.server.DeleteEntry(apis.ChunkNum(request.Chunk), apis.MetadataEntry{
		MostRecentVersion:   apis.Version(request.PreviousEntry.MostRecentVersion),
		LastConsumedVersion: apis.Version(request.PreviousEntry.LastConsumedVersion),
		Replicas:            IntArrayToIDArray(request.PreviousEntry.ServerIDs),
	})
	if owner != "" {
		return &twirp.MetadataCache_DeleteEntry_Result{
			Owner:    string(owner),
			OwnerErr: err.Error(),
		}, nil
	}
	return &twirp.MetadataCache_DeleteEntry_Result{
		Owner: string(owner),
	}, err
}

type proxyTwirpAsMetadataCache struct {
	server twirp.MetadataCache
}

func (p *proxyTwirpAsMetadataCache) NewEntry() (apis.ChunkNum, error) {
	result, err := p.server.NewEntry(context.Background(), &twirp.MetadataCache_NewEntry{})
	if err != nil {
		return 0, err
	}
	return apis.ChunkNum(result.Chunk), nil
}

func (p *proxyTwirpAsMetadataCache) ReadEntry(chunk apis.ChunkNum) (apis.MetadataEntry, apis.ServerName, error) {
	result, err := p.server.ReadEntry(context.Background(), &twirp.MetadataCache_ReadEntry{
		Chunk: uint64(chunk),
	})
	if err != nil {
		return apis.MetadataEntry{}, "", err
	}
	if result.Owner != "" {
		return apis.MetadataEntry{}, apis.ServerName(result.Owner), errors.New(result.OwnerErr)
	}
	return apis.MetadataEntry{
		MostRecentVersion:   apis.Version(result.Entry.MostRecentVersion),
		LastConsumedVersion: apis.Version(result.Entry.LastConsumedVersion),
		Replicas:            IntArrayToIDArray(result.Entry.ServerIDs),
	}, "", nil
}

func (p *proxyTwirpAsMetadataCache) UpdateEntry(chunk apis.ChunkNum, previousEntry apis.MetadataEntry, newEntry apis.MetadataEntry) (apis.ServerName, error) {
	result, err := p.server.UpdateEntry(context.Background(), &twirp.MetadataCache_UpdateEntry{
		Chunk: uint64(chunk),
		PreviousEntry: &twirp.MetadataEntry{
			MostRecentVersion:   uint64(previousEntry.MostRecentVersion),
			LastConsumedVersion: uint64(previousEntry.LastConsumedVersion),
			ServerIDs:           IDArrayToIntArray(previousEntry.Replicas),
		},
		NewEntry: &twirp.MetadataEntry{
			MostRecentVersion:   uint64(newEntry.MostRecentVersion),
			LastConsumedVersion: uint64(newEntry.LastConsumedVersion),
			ServerIDs:           IDArrayToIntArray(newEntry.Replicas),
		},
	})
	if result.Owner != "" {
		return apis.ServerName(result.Owner), errors.New(result.OwnerErr)
	}
	return "", err
}

func (p *proxyTwirpAsMetadataCache) DeleteEntry(chunk apis.ChunkNum, previous apis.MetadataEntry) (apis.ServerName, error) {
	result, err := p.server.DeleteEntry(context.Background(), &twirp.MetadataCache_DeleteEntry{
		Chunk: uint64(chunk),
		PreviousEntry: &twirp.MetadataEntry{
			LastConsumedVersion: uint64(previous.LastConsumedVersion),
			MostRecentVersion:   uint64(previous.MostRecentVersion),
			ServerIDs:           IDArrayToIntArray(previous.Replicas),
		},
	})
	if result.Owner != "" {
		return apis.ServerName(result.Owner), errors.New(result.OwnerErr)
	}
	return apis.ServerName(result.Owner), err
}
