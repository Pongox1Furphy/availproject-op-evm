package block

import (
	"fmt"

	"github.com/0xPolygon/polygon-edge/blockchain"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/maticnetwork/avail-settlement/pkg/staking"
)

type verifier struct {
	activeSequencers staking.ActiveSequencers
	logger           hclog.Logger
}

func NewVerifier(as staking.ActiveSequencers, logger hclog.Logger) blockchain.Verifier {
	return &verifier{
		activeSequencers: as,
		logger:           logger,
	}
}

func (v *verifier) VerifyHeader(header *types.Header) error {
	signer, err := AddressRecoverFromHeader(header)
	if err != nil {
		return err
	}

	v.logger.Info("Verify header", "signer", signer.String())

	minerIsActiveSequencer, err := v.activeSequencers.Contains(signer)
	if err != nil {
		return err
	}

	if !minerIsActiveSequencer {
		return fmt.Errorf("signer address '%s' does not belong to active sequencers", signer)
	}

	v.logger.Info("Seal signer address successfully verified!", "signer", signer)

	/*
		parent, ok := i.blockchain.GetHeaderByNumber(header.Number - 1)
		if !ok {
			return fmt.Errorf(
				"unable to get parent header for block number %d",
				header.Number,
			)
		}

		snap, err := i.getSnapshot(parent.Number)
		if err != nil {
			return err
		}

		// verify all the header fields + seal
		if err := i.verifyHeaderImpl(snap, parent, header); err != nil {
			return err
		}

		// verify the committed seals
		if err := verifyCommittedFields(snap, header, i.quorumSize(header.Number)); err != nil {
			return err
		}

		return nil
	*/
	return nil
}

func (v *verifier) ProcessHeaders(headers []*types.Header) error {
	return nil
}

func (v *verifier) GetBlockCreator(header *types.Header) (types.Address, error) {
	return types.BytesToAddress(header.Miner), nil
}

// PreCommitState a hook to be called before finalizing state transition on inserting block
func (v *verifier) PreCommitState(_header *types.Header, _txn *state.Transition) error {
	return nil
}