package staking

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"time"

	"github.com/0xPolygon/polygon-edge/blockchain"
	edge_crypto "github.com/0xPolygon/polygon-edge/crypto"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	staking_contract "github.com/maticnetwork/avail-settlement-contracts/staking/pkg/staking"
	"github.com/maticnetwork/avail-settlement/pkg/block"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
)

type DisputeResolution interface {
	Get() ([]types.Address, error)
	Contains(addr types.Address) (bool, error)
	GetSequencerAddr(addr types.Address) (types.Address, error)
	GetWatchtowerAddr(addr types.Address) (types.Address, error)
	Begin(probationAddr types.Address, signKey *ecdsa.PrivateKey) error
	End(probationAddr types.Address, signKey *ecdsa.PrivateKey) error
}

type disputeResolution struct {
	blockchain *blockchain.Blockchain
	executor   *state.Executor
	logger     hclog.Logger
	sender     AvailSender
}

func NewDisputeResolution(blockchain *blockchain.Blockchain, executor *state.Executor, sender AvailSender, logger hclog.Logger) DisputeResolution {
	return &disputeResolution{
		blockchain: blockchain,
		executor:   executor,
		logger:     logger.ResetNamed("staking_dispute_resolution"),
		sender:     sender,
	}
}

func (dr *disputeResolution) Get() ([]types.Address, error) {
	parent := dr.blockchain.Header()
	minerAddress := types.BytesToAddress(parent.Miner)

	header := &types.Header{
		ParentHash: parent.Hash,
		Number:     parent.Number + 1,
		Miner:      minerAddress.Bytes(),
		Nonce:      types.Nonce{},
		GasLimit:   parent.GasLimit, // Inherit from parent for now, will need to adjust dynamically later.
		Timestamp:  uint64(time.Now().Unix()),
	}

	// calculate gas limit based on parent header
	gasLimit, err := dr.blockchain.CalculateGasLimit(header.Number)
	if err != nil {
		return nil, err
	}

	transition, err := dr.executor.BeginTxn(parent.StateRoot, header, minerAddress)
	if err != nil {
		return nil, err
	}

	probationAddrs, err := QuerySequencersInProbation(transition, gasLimit, minerAddress)
	if err != nil {
		return nil, err
	}

	return probationAddrs, nil
}

func (dr *disputeResolution) Contains(addr types.Address) (bool, error) {
	addrs, err := dr.Get()
	if err != nil {
		return false, err
	}

	for _, a := range addrs {
		if a == addr {
			return true, nil
		}
	}

	return false, nil
}

func (dr *disputeResolution) GetSequencerAddr(watchtowerAddr types.Address) (types.Address, error) {
	parent := dr.blockchain.Header()
	minerAddress := types.BytesToAddress(parent.Miner)

	dr.logger.Info("Got addresses", "Miner", minerAddress.String(), "Watchtower", watchtowerAddr.String())

	header := &types.Header{
		ParentHash: parent.Hash,
		Number:     parent.Number + 1,
		Miner:      minerAddress.Bytes(),
		Nonce:      types.Nonce{},
		GasLimit:   parent.GasLimit, // Inherit from parent for now, will need to adjust dynamically later.
		Timestamp:  uint64(time.Now().Unix()),
	}

	// calculate gas limit based on parent header
	gasLimit, err := dr.blockchain.CalculateGasLimit(header.Number)
	if err != nil {
		return types.Address{}, err
	}

	transition, err := dr.executor.BeginTxn(parent.StateRoot, header, minerAddress)
	if err != nil {
		return types.Address{}, err
	}

	sequencerAddr, err := QueryDisputedSequencerAddr(transition, gasLimit, minerAddress, watchtowerAddr)
	if err != nil {
		return types.Address{}, err
	}

	return sequencerAddr, nil
}

func (dr *disputeResolution) GetWatchtowerAddr(sequencerAddr types.Address) (types.Address, error) {
	parent := dr.blockchain.Header()
	minerAddress := types.BytesToAddress(parent.Miner)

	header := &types.Header{
		ParentHash: parent.Hash,
		Number:     parent.Number + 1,
		Miner:      minerAddress.Bytes(),
		Nonce:      types.Nonce{},
		GasLimit:   parent.GasLimit, // Inherit from parent for now, will need to adjust dynamically later.
		Timestamp:  uint64(time.Now().Unix()),
	}

	// calculate gas limit based on parent header
	gasLimit, err := dr.blockchain.CalculateGasLimit(header.Number)
	if err != nil {
		return types.Address{}, err
	}

	transition, err := dr.executor.BeginTxn(parent.StateRoot, header, minerAddress)
	if err != nil {
		return types.Address{}, err
	}

	watchtowerAddr, err := QueryDisputedWatchtowerAddr(transition, gasLimit, minerAddress, sequencerAddr)
	if err != nil {
		return types.Address{}, err
	}

	return watchtowerAddr, nil
}

func (dr *disputeResolution) Begin(probationAddr types.Address, signKey *ecdsa.PrivateKey) error {
	builder := block.NewBlockBuilderFactory(dr.blockchain, dr.executor, dr.logger)
	blk, err := builder.FromBlockchainHead()
	if err != nil {
		return err
	}

	pk := signKey.Public().(*ecdsa.PublicKey)
	address := edge_crypto.PubKeyToAddress(pk)

	blk.SetCoinbaseAddress(address)
	blk.SignWith(signKey)

	disputeResolutionTx, err := BeginDisputeResolutionTx(address, probationAddr, dr.blockchain.Header().GasLimit)
	if err != nil {
		dr.logger.Error("failed to begin new fraud dispute resolution", "err", err)
		return err
	}

	blk.AddTransactions(disputeResolutionTx)

	fBlock, err := blk.Build()
	if err != nil {
		return err
	}

	if err := dr.sender.Send(fBlock); err != nil {
		return err
	}

	if err := dr.blockchain.WriteBlock(fBlock, "staking_fraud_dispute_resolution_modifier"); err != nil {
		return err
	}

	return nil
}

func (dr *disputeResolution) End(probationAddr types.Address, signKey *ecdsa.PrivateKey) error {
	builder := block.NewBlockBuilderFactory(dr.blockchain, dr.executor, dr.logger)
	blk, err := builder.FromBlockchainHead()
	if err != nil {
		return err
	}

	pk := signKey.Public().(*ecdsa.PublicKey)
	address := edge_crypto.PubKeyToAddress(pk)

	blk.SetCoinbaseAddress(address)
	blk.SignWith(signKey)

	disputeResolutionTx, err := EndDisputeResolutionTx(address, probationAddr, dr.blockchain.Header().GasLimit)
	if err != nil {
		dr.logger.Error("failed to end new fraud dispute resolution", "err", err)
		return err
	}

	blk.AddTransactions(disputeResolutionTx)

	fBlock, err := blk.Build()
	if err != nil {
		return err
	}

	if err := dr.sender.Send(fBlock); err != nil {
		return err
	}

	if err := dr.blockchain.WriteBlock(fBlock, "staking_fraud_dispute_resolution_modifier"); err != nil {
		return err
	}

	return nil
}

func BeginDisputeResolutionTx(from types.Address, probationAddr types.Address, gasLimit uint64) (*types.Transaction, error) {
	method, ok := abi.MustNewABI(staking_contract.StakingABI).Methods["BeginDisputeResolution"]
	if !ok {
		panic("BeginDisputeResolution method doesn't exist in Staking contract ABI. Contract is broken.")
	}

	selector := method.ID()

	encodedInput, encodeErr := method.Inputs.Encode(
		map[string]interface{}{
			"sequencerAddr": probationAddr.Bytes(),
		},
	)
	if encodeErr != nil {
		return nil, encodeErr
	}

	return &types.Transaction{
		From:     from,
		To:       &AddrStakingContract,
		Value:    big.NewInt(0),
		Input:    append(selector, encodedInput...),
		GasPrice: big.NewInt(5000),
		Gas:      gasLimit,
	}, nil
}

func EndDisputeResolutionTx(from types.Address, probationAddr types.Address, gasLimit uint64) (*types.Transaction, error) {
	method, ok := abi.MustNewABI(staking_contract.StakingABI).Methods["EndDisputeResolution"]
	if !ok {
		panic("EndDisputeResolution method doesn't exist in Staking contract ABI. Contract is broken.")
	}

	selector := method.ID()

	encodedInput, encodeErr := method.Inputs.Encode(
		map[string]interface{}{
			"sequencerAddr": probationAddr.Bytes(),
		},
	)
	if encodeErr != nil {
		return nil, encodeErr
	}

	return &types.Transaction{
		From:     from,
		To:       &AddrStakingContract,
		Value:    big.NewInt(0),
		Input:    append(selector, encodedInput...),
		GasPrice: big.NewInt(5000),
		Gas:      gasLimit,
	}, nil
}

func QueryDisputedSequencerAddr(t *state.Transition, gasLimit uint64, from types.Address, watchtowerAddr types.Address) (types.Address, error) {
	method, ok := abi.MustNewABI(staking_contract.StakingABI).Methods["GetDisputedSequencerAddrs"]
	if !ok {
		return types.Address{}, errors.New("GetDisputedSequencerAddrs method doesn't exist in Staking contract ABI")
	}

	encodedInput, encodeErr := method.Inputs.Encode(
		map[string]interface{}{
			"watchtowerAddr": watchtowerAddr.Bytes(),
		},
	)
	if encodeErr != nil {
		return types.Address{}, encodeErr
	}

	selector := method.ID()
	res, err := t.Apply(&types.Transaction{
		From:     from,
		To:       &AddrStakingContract,
		Value:    big.NewInt(0),
		Input:    append(selector, encodedInput...),
		GasPrice: big.NewInt(5000),
		Gas:      gasLimit,
		Nonce:    t.GetNonce(from),
	})

	if err != nil {
		return types.Address{}, err
	}

	if res.Failed() {
		return types.Address{}, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return types.Address{}, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return types.Address{}, errors.New("failed type assertion from decodedResults to map")
	}

	address, _ := results["0"].(ethgo.Address)
	return types.Address(address), nil
}

func QueryDisputedWatchtowerAddr(t *state.Transition, gasLimit uint64, from types.Address, sequencerAddr types.Address) (types.Address, error) {
	method, ok := abi.MustNewABI(staking_contract.StakingABI).Methods["GetDisputedWatchtowerAddr"]
	if !ok {
		return types.Address{}, errors.New("GetDisputedWatchtowerAddr method doesn't exist in Staking contract ABI")
	}

	encodedInput, encodeErr := method.Inputs.Encode(
		map[string]interface{}{
			"sequencerAddr": sequencerAddr.Bytes(),
		},
	)
	if encodeErr != nil {
		return types.Address{}, encodeErr
	}

	selector := method.ID()
	res, err := t.Apply(&types.Transaction{
		From:     from,
		To:       &AddrStakingContract,
		Value:    big.NewInt(0),
		Input:    append(selector, encodedInput...),
		GasPrice: big.NewInt(5000),
		Gas:      gasLimit,
		Nonce:    t.GetNonce(from),
	})

	if err != nil {
		return types.Address{}, err
	}

	if res.Failed() {
		return types.Address{}, res.Err
	}

	decodedResults, err := method.Outputs.Decode(res.ReturnValue)
	if err != nil {
		return types.Address{}, err
	}

	results, ok := decodedResults.(map[string]interface{})
	if !ok {
		return types.Address{}, errors.New("failed type assertion from decodedResults to map")
	}

	address, _ := results["0"].(ethgo.Address)
	return types.Address(address), nil
}