package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	tendermint "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/ghodss/yaml"
)

const (
	configPath    = "./config/config.yaml"
	targetChainID = "osmosis-1"
	maxWorkers    = 1000 // Maximum number of goroutines that will run concurrently while querying escrow information.
)

var (
	// targetChannels should be an empty slice if you want to run the escrow checker against every escrow account.
	// Otherwise, add the channel-ids associated with escrow accounts you want to target.
	targetChannels = []string{}

	retries       = uint(7)
	retryAttempts = retry.Attempts(retries)
	retryDelay    = retry.Delay(time.Millisecond * 1000)
	retryError    = retry.LastErrorOnly(true)
)

type Info struct {
	Channel             *chantypes.IdentifiedChannel
	EscrowAddress       string
	Balances            sdktypes.Coins
	CounterpartyChainID string
}

func main() {
	cfg, err := readConfig(configPath)
	if err != nil {
		panic(err)
	}

	clients, err := clientsFromConfig(cfg)
	if err != nil {
		panic(err)
	}

	c, err := clients.clientByChainID(targetChainID)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	fmt.Println("Querying channels...")

	var channels []*chantypes.IdentifiedChannel

	if err := retry.Do(func() error {
		channels, err = queryChannels(ctx, c)
		return err
	}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
		fmt.Printf("Failed to query channels, retrying (%d/%d): %s \n", n+1, retries, err.Error())
	})); err != nil {
		panic(err)
	}

	fmt.Printf("Number of channels: %d \n", len(channels))

	// For every channel query the associated escrow account address, the escrow account balances,
	// and the channel's associated client state in order to identify the counterparty chain.
	var (
		sem   = make(chan struct{}, maxWorkers)
		wg    = sync.WaitGroup{}
		mu    = sync.Mutex{}
		infos = make([]*Info, 0)
	)

	fmt.Println("Querying escrow account information for each channel...")
	for i, channel := range channels {
		channel := channel
		i := i

		wg.Add(1)
		sem <- struct{}{}

		fmt.Printf("Starting worker number %d for channel %s \n", i+1, channel.ChannelId)

		go func() {
			defer func() {
				wg.Done()
				<-sem
			}()

			var (
				addr string
				bals *banktypes.QueryAllBalancesResponse
				res  *chantypes.QueryChannelClientStateResponse
			)

			if err := retry.Do(func() error {
				addr, err = c.QueryEscrowAddress(ctx, channel.PortId, channel.ChannelId)
				return err
			}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
				fmt.Printf("Failed to query escrow address for %s, retrying (%d/%d): %s \n", channel.ChannelId, n+1, retries, err.Error())
			})); err != nil {
				panic(err)
			}

			if err := retry.Do(func() error {
				bals, err = c.QueryBalances(ctx, addr)
				return err
			}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
				fmt.Printf("Failed to query escrow balance for %s, retrying (%d/%d): %s \n", addr, n+1, retries, err.Error())
			})); err != nil {
				panic(err)
			}

			if err = retry.Do(func() error {
				res, err = c.QueryChannelClientState(channel.PortId, channel.ChannelId)
				return err
			}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
				fmt.Printf("Failed to query channel client state for %s, retrying (%d/%d): %s \n", channel.ChannelId, n+1, retries, err.Error())
			})); err != nil {
				panic(err)
			}

			cs := &tendermint.ClientState{}
			err = proto.Unmarshal(res.IdentifiedClientState.ClientState.Value, cs)
			if err != nil {
				panic(err)
			}

			mu.Lock()
			infos = append(infos, &Info{
				Channel:             channel,
				EscrowAddress:       addr,
				Balances:            bals.Balances,
				CounterpartyChainID: cs.ChainId,
			})
			mu.Unlock()
		}()
	}

	wg.Wait()
	fmt.Println("Finished querying escrow account information.")

	// For each token balance in the escrow accounts, query the IBC denom trace from the hash,
	// then compose the denom on the counterparty chain and query the tokens total supply.
	// Assert that the balance in the escrow account is equal to the total supply on the counterparty.
	fmt.Println("Querying counterparty total supply for each token found in an escrow account...")

	for _, info := range infos {
		client, err := clients.clientByChainID(info.CounterpartyChainID)
		if err != nil {
			fmt.Println(err)
			continue
		}

		for _, bal := range info.Balances {
			var (
				hash   string
				denom  *transfertypes.DenomTrace
				amount sdktypes.Coin
			)

			if strings.Contains(bal.Denom, "ibc/") {
				parts := strings.Split(bal.Denom, "/")
				hash = parts[1]
			} else {
				// Found a native denom or non-IBC denom.
				continue
			}

			if err := retry.Do(func() error {
				denom, err = c.QueryDenomTrace(ctx, hash)
				return err
			}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
				fmt.Printf("Failed to query denom trace for %s, retrying (%d/%d): %s \n", hash, n+1, retries, err.Error())
			})); err != nil {
				panic(err)
			}

			path := fmt.Sprintf("%s/%s/%s", info.Channel.Counterparty.PortId, info.Channel.Counterparty.ChannelId, denom.Path)
			counterpartyDenom := transfertypes.ParseDenomTrace(fmt.Sprintf("%s/%s", path, denom.BaseDenom))

			ibcDenom := counterpartyDenom.IBCDenom()
			if err := retry.Do(func() error {
				amount, err = client.QueryBankTotalSupply(ctx, ibcDenom)
				return err
			}, retry.Context(ctx), retryAttempts, retryDelay, retryError, retry.OnRetry(func(n uint, err error) {
				fmt.Printf("Failed to query total supply of %s, retrying (%d/%d): %s \n", ibcDenom, n+1, retries, err.Error())
			})); err != nil {
				panic(err)
			}

			if !bal.Amount.Equal(amount.Amount) {
				fmt.Println("--------------------------------------------")
				fmt.Println("Discrepancy found!")
				fmt.Printf("Counterparty Chain ID: %s \n", info.CounterpartyChainID)
				fmt.Printf("Escrow Account Address: %s \n", info.EscrowAddress)
				fmt.Printf("Asset Base Denom: %s \n", denom.BaseDenom)
				fmt.Printf("Asset IBC Denom: %s \n", bal.Denom)
				fmt.Printf("Escrow Balance: %s \n", bal.Amount)
				fmt.Printf("Counterparty Total Supply: %s \n", amount)
			}
		}
	}
}

func readConfig(path string) (*Config, error) {
	cfgFile, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	err = yaml.Unmarshal(cfgFile, cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func clientsFromConfig(cfg *Config) (Clients, error) {
	clients := make([]*Client, len(cfg.Chains))

	for i, c := range cfg.Chains {
		t, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return nil, err
		}

		clients[i] = NewClient(c.ChainID, c.RPCAddress, c.AccountPrefix, t)
	}

	return clients, nil
}

func queryChannels(ctx context.Context, c *Client) ([]*chantypes.IdentifiedChannel, error) {
	var (
		channels []*chantypes.IdentifiedChannel
		err      error
	)

	if len(targetChannels) == 0 {
		channels, err = c.QueryChannels(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		for _, id := range targetChannels {
			channel, err := c.QueryChannel(ctx, id)
			if err != nil {
				return nil, err
			}

			channels = append(channels, channel)
		}
	}

	return channels, nil
}
