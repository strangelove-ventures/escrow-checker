package main

import (
	"context"
	"strings"
	"time"

	"cosmossdk.io/store/rootmulti"
	abci "github.com/cometbft/cometbft/abci/types"
	client2 "github.com/cometbft/cometbft/rpc/client"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	legacyerrors "github.com/cosmos/cosmos-sdk/types/errors"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const paginationDelay = 10 * time.Millisecond

func (c *Client) QueryBalances(ctx context.Context, addr string) (*banktypes.QueryAllBalancesResponse, error) {
	qc := banktypes.NewQueryClient(c)

	req := &banktypes.QueryAllBalancesRequest{
		Address:      addr,
		Pagination:   defaultPageRequest(),
		ResolveDenom: true,
	}

	res, err := qc.AllBalances(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (c *Client) QueryBalance(ctx context.Context, addr, denom string) (sdktypes.Coin, error) {
	qc := banktypes.NewQueryClient(c)

	req := &banktypes.QueryBalanceRequest{
		Address: addr,
		Denom:   denom,
	}

	res, err := qc.Balance(ctx, req, nil)
	if err != nil {
		return sdktypes.Coin{}, err
	}

	return *res.Balance, nil
}

func (c *Client) QueryBankTotalSupply(ctx context.Context, denom string) (sdktypes.Coin, error) {
	var (
		qc  = banktypes.NewQueryClient(c)
		req = &banktypes.QuerySupplyOfRequest{Denom: denom}
	)

	res, err := qc.SupplyOf(ctx, req, nil)
	if err != nil {
		return sdktypes.Coin{}, err
	}

	return res.Amount, nil
}

func (c *Client) QueryEscrowAddress(ctx context.Context, portID, channelID string) (string, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryEscrowAddressRequest{
		PortId:    portID,
		ChannelId: channelID,
	}

	res, err := qc.EscrowAddress(ctx, req, nil)
	if err != nil {
		return "", err
	}

	return res.EscrowAddress, nil
}

func (c *Client) QueryTotalEscrowForDenom(ctx context.Context, denom string) (sdktypes.Coin, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryTotalEscrowForDenomRequest{
		Denom: denom,
	}

	res, err := qc.TotalEscrowForDenom(ctx, req)
	if err != nil {
		return sdktypes.Coin{}, err
	}

	return res.Amount, nil
}

func (c *Client) QueryEscrowAmount(ctx context.Context, denom string) (sdktypes.Coin, error) {
	var (
		qc  = transfertypes.NewQueryClient(c)
		req = &transfertypes.QueryTotalEscrowForDenomRequest{Denom: denom}
	)

	res, err := qc.TotalEscrowForDenom(ctx, req, nil)
	if err != nil {
		return sdktypes.Coin{}, err
	}

	return res.Amount, nil
}

func (c *Client) QueryDenomHash(ctx context.Context, denomTrace string) (string, error) {
	qc := transfertypes.NewQueryClient(c)

	req := &transfertypes.QueryDenomHashRequest{
		Trace: denomTrace,
	}

	res, err := qc.DenomHash(ctx, req, nil)
	if err != nil {
		return "", err
	}

	return res.Hash, nil
}

func (c *Client) QueryDenomTrace(ctx context.Context, hash string) (*transfertypes.DenomTrace, error) {
	var (
		qc  = transfertypes.NewQueryClient(c)
		req = &transfertypes.QueryDenomTraceRequest{Hash: hash}
	)

	res, err := qc.DenomTrace(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	return res.DenomTrace, nil
}

func (c *Client) QueryDenomTraces(ctx context.Context, offset, limit uint64, height int64) ([]transfertypes.DenomTrace, error) {
	var (
		qc        = transfertypes.NewQueryClient(c)
		p         = defaultPageRequest()
		transfers []transfertypes.DenomTrace
	)

	for {
		res, err := qc.DenomTraces(ctx,
			&transfertypes.QueryDenomTracesRequest{
				Pagination: p,
			},
		)

		if err != nil || res == nil {
			return nil, err
		}

		transfers = append(transfers, res.DenomTraces...)

		next := res.GetPagination().GetNextKey()
		if len(next) == 0 {
			break
		}

		time.Sleep(paginationDelay)
		p.Key = next
	}

	return transfers, nil
}

func (c *Client) QueryChannelClientState(
	portID string,
	channelID string,
) (*chantypes.QueryChannelClientStateResponse, error) {
	qc := chantypes.NewQueryClient(c)

	req := &types.QueryChannelClientStateRequest{
		PortId:    portID,
		ChannelId: channelID,
	}

	res, err := qc.ChannelClientState(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// QueryChannels returns all the channels that are registered on a chain.
func (c *Client) QueryChannels(ctx context.Context) ([]*chantypes.IdentifiedChannel, error) {
	p := defaultPageRequest()
	var chans []*chantypes.IdentifiedChannel

	for {
		res, next, err := c.QueryChannelsPaginated(ctx, p)
		if err != nil {
			return nil, err
		}

		chans = append(chans, res...)
		if len(next) == 0 {
			break
		}

		time.Sleep(paginationDelay)
		p.Key = next
	}

	return chans, nil
}

// QueryChannelsPaginated returns all the channels for a particular paginated request that are registered on a chain.
func (c *Client) QueryChannelsPaginated(
	ctx context.Context,
	pageRequest *querytypes.PageRequest,
) ([]*chantypes.IdentifiedChannel, []byte, error) {
	qc := chantypes.NewQueryClient(c)

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	res, err := qc.Channels(ctx, &chantypes.QueryChannelsRequest{
		Pagination: pageRequest,
	})
	if err != nil {
		return nil, nil, err
	}

	next := res.GetPagination().GetNextKey()

	return res.Channels, next, nil
}

// QueryABCI performs an ABCI query and returns the appropriate response and error sdk error code.
func (c *Client) QueryABCI(ctx context.Context, req abci.RequestQuery) (abci.ResponseQuery, error) {
	opts := client2.ABCIQueryOptions{
		Height: req.Height,
		Prove:  req.Prove,
	}

	result, err := c.RPCClient.ABCIQueryWithOptions(ctx, req.Path, req.Data, opts)
	if err != nil {
		return abci.ResponseQuery{}, err
	}

	if !result.Response.IsOK() {
		return abci.ResponseQuery{}, sdkErrorToGRPCError(result.Response)
	}

	// data from trusted node or subspace query doesn't need verification
	if !opts.Prove || !isQueryStoreWithProof(req.Path) {
		return result.Response, nil
	}

	return result.Response, nil
}

func sdkErrorToGRPCError(resp abci.ResponseQuery) error {
	switch resp.Code {
	case legacyerrors.ErrInvalidRequest.ABCICode():
		return status.Error(codes.InvalidArgument, resp.Log)
	case legacyerrors.ErrUnauthorized.ABCICode():
		return status.Error(codes.Unauthenticated, resp.Log)
	case legacyerrors.ErrKeyNotFound.ABCICode():
		return status.Error(codes.NotFound, resp.Log)
	default:
		return status.Error(codes.Unknown, resp.Log)
	}
}

// isQueryStoreWithProof expects a format like /<queryType>/<storeName>/<subpath>
// queryType must be "store" and subpath must be "key" to require a proof.
func isQueryStoreWithProof(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}

	paths := strings.SplitN(path[1:], "/", 3)

	switch {
	case len(paths) != 3:
		return false
	case paths[0] != "store":
		return false
	case rootmulti.RequireProof("/" + paths[2]):
		return true
	}

	return false
}

func defaultPageRequest() *querytypes.PageRequest {
	return &querytypes.PageRequest{
		Key:        []byte(""),
		Offset:     0,
		Limit:      1000,
		CountTotal: false,
	}
}
