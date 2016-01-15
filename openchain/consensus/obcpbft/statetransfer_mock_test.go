/*
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
*/

package obcpbft

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/openblockchain/obc-peer/openchain/consensus"
	"github.com/openblockchain/obc-peer/protos"
)

type mockRequest int

const (
	SyncDeltas mockRequest = iota
	SyncBlocks
	SyncSnapshot
)

type mockResponse int

const (
	Normal mockResponse = iota
	Corrupt
	Timeout
)

func (r mockResponse) String() string {
	switch r {
	case Normal:
		return "Normal"
	case Corrupt:
		return "Corrupt"
	case Timeout:
		return "Timeout"
	}

	return "ERROR"
}

type MockLedger struct {
	cleanML       *MockLedger
	blocks        map[uint64]*protos.Block
	blockHeight   uint64
	state         uint64
	remoteLedgers *map[uint64]consensus.ReadOnlyLedger
	filter        func(request mockRequest, replicaID uint64) mockResponse

	mutex *sync.Mutex

	txID     interface{}
	curBatch []*protos.Transaction

	inst *instance // To support the ExecTX stuff
}

func NewMockLedger(remoteLedgers *map[uint64]consensus.ReadOnlyLedger, filter func(request mockRequest, replicaID uint64) mockResponse) *MockLedger {
	mock := &MockLedger{}
	mock.mutex = &sync.Mutex{}
	mock.blocks = make(map[uint64]*protos.Block)
	mock.state = 0
	mock.blockHeight = 0

	if nil == filter {
		mock.filter = func(request mockRequest, replicaID uint64) mockResponse {
			return Normal
		}
	} else {
		mock.filter = filter
	}

	/* // This might be useful to add back
	if nil == remoteLedgers {
		mock.remoteLedgers = make(map[uint64]consensus.ReadOnlyLedger)
		DummyLedger := &MockRemoteLedger{^uint64(0)}
		for i := uint64(0); i < 100; i++ {
			mock.remoteLedgers[i] = DummyLedger
		}
	} else {
		mock.remoteLedgers = remoteLedgers
	}
	*/
	mock.remoteLedgers = remoteLedgers

	return mock
}

func (mock *MockLedger) BeginTxBatch(id interface{}) error {
	if mock.txID != nil {
		return fmt.Errorf("Tx batch is already active")
	}
	mock.txID = id
	mock.curBatch = nil
	return nil
}

func (mock *MockLedger) ExecTXs(txs []*protos.Transaction) ([]byte, []error) {
	mock.curBatch = append(mock.curBatch, txs...)
	errs := make([]error, len(txs)+1)
	if mock.inst.execTxResult != nil {
		return mock.inst.execTxResult(txs)
	}
	return nil, errs
}

func (mock *MockLedger) CommitTxBatch(id interface{}, txs []*protos.Transaction, txResults []*protos.TransactionResult, metadata []byte) error {
	_, err := mock.commonCommitTx(id, txs, txResults, metadata, false)
	if nil == err {
		mock.txID = nil
	}
	return err
}

func (mock *MockLedger) commonCommitTx(id interface{}, txs []*protos.Transaction, txResults []*protos.TransactionResult, metadata []byte, preview bool) (*protos.Block, error) {
	if !reflect.DeepEqual(mock.txID, id) {
		return nil, fmt.Errorf("Invalid batch ID")
	}
	if !reflect.DeepEqual(txs, mock.curBatch) {
		return nil, fmt.Errorf("Tx list does not match executed Tx batch")
	}

	previousBlockHash := []byte("Genesis")
	if 0 < mock.blockHeight {
		previousBlock, _ := mock.GetBlock(mock.blockHeight - 1)
		previousBlockHash, _ = mock.HashBlock(previousBlock)
	}

	buffer := make([]byte, binary.MaxVarintLen64)

	if nil == txs {
		txs = []*protos.Transaction{&protos.Transaction{Payload: SimpleGetStateDelta(mock.blockHeight)}}
	}

	for _, transaction := range txs {
		if transaction.Payload == nil {
			transaction.Payload = SimpleGetStateDelta(mock.blockHeight)
		}

		for i, b := range transaction.Payload {
			buffer[i%binary.MaxVarintLen64] += b
		}
	}

	mock.ApplyStateDelta(buffer, false)

	stateHash, _ := mock.GetCurrentStateHash()

	block := &protos.Block{
		Transactions:      txs,
		ConsensusMetadata: metadata,
		PreviousBlockHash: previousBlockHash,
		StateHash:         stateHash,
	}

	if preview {
		mock.ApplyStateDelta(buffer, true)
	} else {
		fmt.Printf("Debug: Mock ledger is inserting block %d with hash %v\n", mock.blockHeight, SimpleHashBlock(block))
		mock.PutBlock(mock.blockHeight, block)
	}

	return block, nil
}

func (mock *MockLedger) PreviewCommitTxBatchBlock(id interface{}, txs []*protos.Transaction, metadata []byte) (*protos.Block, error) {
	return mock.commonCommitTx(id, txs, nil, metadata, true)
}

func (mock *MockLedger) RollbackTxBatch(id interface{}) error {
	if !reflect.DeepEqual(mock.txID, id) {
		return fmt.Errorf("Invalid batch ID")
	}
	mock.curBatch = nil
	mock.txID = nil
	return nil
}

func (mock *MockLedger) GetBlockchainSize() (uint64, error) {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	return mock.blockHeight, nil
}

func (mock *MockLedger) GetBlock(id uint64) (*protos.Block, error) {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	block, ok := mock.blocks[id]
	if !ok {
		return nil, fmt.Errorf("Block not found")
	}
	return block, nil
}

func (mock *MockLedger) HashBlock(block *protos.Block) ([]byte, error) {
	return SimpleHashBlock(block), nil
}

func (mock *MockLedger) GetRemoteBlocks(replicaID uint64, start, finish uint64) (<-chan *protos.SyncBlocks, error) {
	res := make(chan *protos.SyncBlocks)
	ft := mock.filter(SyncBlocks, replicaID)
	switch ft {
	case Corrupt:
		fallthrough
	case Normal:
		go func() {
			current := start
			corruptBlock := start + (finish - start/2) // Try to pick a block in the middle, if possible

			for {
				if ft != Corrupt || current != corruptBlock {
					if block, err := (*mock.remoteLedgers)[replicaID].GetBlock(current); nil == err {
						res <- &protos.SyncBlocks{
							Range: &protos.SyncBlockRange{
								Start: current,
								End:   current,
							},
							Blocks: []*protos.Block{block},
						}

					} else {
						break
					}
				} else {
					res <- &protos.SyncBlocks{
						Range: &protos.SyncBlockRange{
							Start: current,
							End:   current,
						},
						Blocks: []*protos.Block{&protos.Block{
							PreviousBlockHash: []byte("GARBAGE_BLOCK_HASH"),
							StateHash:         []byte("GARBAGE_STATE_HASH"),
							Transactions: []*protos.Transaction{
								&protos.Transaction{
									Payload: []byte("GARBAGE_PAYLOAD"),
								},
							},
						}},
					}
				}

				if current == finish {
					break
				}

				if start < finish {
					current++
				} else {
					current--
				}
			}
			close(res)
		}()
	case Timeout:
	default:
		return nil, fmt.Errorf("Unsupported filter result %d", ft)
	}

	return res, nil
}

func (mock *MockLedger) GetRemoteStateSnapshot(replicaID uint64) (<-chan *protos.SyncStateSnapshot, error) {
	res := make(chan *protos.SyncStateSnapshot)
	ft := mock.filter(SyncSnapshot, replicaID)
	switch ft {
	case Corrupt:
		fallthrough
	case Normal:

		remoteBlockHeight, _ := (*mock.remoteLedgers)[replicaID].GetBlockchainSize()
		if remoteBlockHeight < 1 {
			break
		}
		rds, err := mock.GetRemoteStateDeltas(replicaID, 0, remoteBlockHeight-1)
		if nil != err {
			return nil, err
		}
		go func() {
			if Corrupt == ft {
				res <- &protos.SyncStateSnapshot{
					Delta:       []byte("GARBAGE_DELTA"),
					Sequence:    0,
					BlockNumber: ^uint64(0),
					Request:     nil,
				}
			}

			i := uint64(0)
			for deltas := range rds {
				for _, delta := range deltas.Deltas {
					res <- &protos.SyncStateSnapshot{
						Delta:       delta,
						Sequence:    i,
						BlockNumber: remoteBlockHeight - 1,
						Request:     nil,
					}
					i++
				}
			}
			close(res)
		}()
	case Timeout:
	default:
		return nil, fmt.Errorf("Unsupported filter result %d", ft)
	}
	return res, nil
}

func (mock *MockLedger) GetRemoteStateDeltas(replicaID uint64, start, finish uint64) (<-chan *protos.SyncStateDeltas, error) {
	res := make(chan *protos.SyncStateDeltas)
	ft := mock.filter(SyncDeltas, replicaID)
	switch ft {
	case Corrupt:
		fallthrough
	case Normal:
		go func() {
			current := start
			corruptBlock := start + (finish - start/2) // Try to pick a block in the middle, if possible
			for {
				if ft != Corrupt || current != corruptBlock {
					if remoteBlock, err := (*mock.remoteLedgers)[replicaID].GetBlock(current); nil == err {
						deltas := make([][]byte, len(remoteBlock.Transactions))
						for i, transaction := range remoteBlock.Transactions {
							deltas[i] = transaction.Payload
						}
						res <- &protos.SyncStateDeltas{
							Range: &protos.SyncBlockRange{
								Start: current,
								End:   current,
							},
							Deltas: deltas,
						}
					} else {
						break
					}
				} else {
					deltas := [][]byte{
						[]byte("GARBAGE_DELTA"),
					}
					res <- &protos.SyncStateDeltas{
						Range: &protos.SyncBlockRange{
							Start: current,
							End:   current,
						},
						Deltas: deltas,
					}

				}

				if current == finish {
					break
				}

				if start < finish {
					current++
				} else {
					current--
				}
			}
			close(res)
		}()
	case Timeout:
	default:
		return nil, fmt.Errorf("Unsupported filter result %d", ft)
	}
	return res, nil
}

func (mock *MockLedger) PutBlock(blockNumber uint64, block *protos.Block) error {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	mock.blocks[blockNumber] = block
	if blockNumber >= mock.blockHeight {
		mock.blockHeight = blockNumber + 1
	}
	return nil
}

func (mock *MockLedger) ApplyStateDelta(delta []byte, unapply bool) error {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	d, r := binary.Uvarint(delta)
	if r <= 0 {
		return fmt.Errorf("State delta could not be applied, was not a uint64, %x", delta)
	}
	if !unapply {
		mock.state += d
	} else {
		mock.state -= d
	}
	return nil
}

func (mock *MockLedger) EmptyState() error {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	mock.state = 0
	return nil
}

func (mock *MockLedger) GetCurrentStateHash() ([]byte, error) {
	mock.mutex.Lock()
	defer func() {
		mock.mutex.Unlock()
	}()
	return []byte(fmt.Sprintf("%d", mock.state)), nil
}

func (mock *MockLedger) VerifyBlockchain(start, finish uint64) (uint64, error) {
	current := start
	for {
		if current == finish {
			return 0, nil
		}

		cb, err := mock.GetBlock(current)

		if nil != err {
			return current, err
		}

		next := current

		if start < finish {
			next++
		} else {
			next--
		}

		nb, err := mock.GetBlock(next)

		if nil != err {
			return current, err
		}

		nbh, err := mock.HashBlock(nb)

		if nil != err {
			return current, err
		}

		if !bytes.Equal(nbh, cb.PreviousBlockHash) {
			return current, nil
		}

		current = next
	}
}

// Used when the actual transaction content is irrelevant, useful for testing
// state transfer, and other situations without requiring a simulated network
type MockRemoteLedger struct {
	blockHeight uint64
}

func (mock *MockRemoteLedger) setBlockHeight(blockHeight uint64) {
	mock.blockHeight = blockHeight
}

func (mock *MockRemoteLedger) GetBlock(blockNumber uint64) (block *protos.Block, err error) {
	if blockNumber >= mock.blockHeight {
		return nil, fmt.Errorf("Request block above block height")
	}
	return SimpleGetBlock(blockNumber), nil
}

func (mock *MockRemoteLedger) GetBlockchainSize() (uint64, error) {
	return mock.blockHeight, nil
}

func (mock *MockRemoteLedger) GetCurrentStateHash() (stateHash []byte, err error) {
	return SimpleEncodeUint64(SimpleGetState(mock.blockHeight - 1)), nil
}

func SimpleEncodeUint64(num uint64) []byte {
	result := make([]byte, binary.MaxVarintLen64)
	binary.PutUvarint(result, num)
	return result
}

func SimpleHashBlock(block *protos.Block) []byte {
	buffer := make([]byte, binary.MaxVarintLen64)
	for _, transaction := range block.Transactions {
		for i, b := range transaction.Payload {
			buffer[i%binary.MaxVarintLen64] += b
		}
	}
	return []byte(fmt.Sprintf("BlockHash:%s-%s-%s", buffer, block.StateHash, block.ConsensusMetadata))
}

func SimpleGetState(blockNumber uint64) uint64 {
	// The simple state is (blockNumber) * (blockNumber + 1) / 2
	var computedState uint64
	if 0 == blockNumber%2 {
		computedState = blockNumber / 2 * (blockNumber + 1)
	} else {
		computedState = (blockNumber + 1) / 2 * blockNumber
	}
	return computedState
}

func SimpleGetStateDelta(blockNumber uint64) []byte {
	return SimpleEncodeUint64(blockNumber)
}

func SimpleGetStateHash(blockNumber uint64) []byte {
	return []byte(fmt.Sprintf("%d", SimpleGetState(blockNumber)))
}

func SimpleGetTransactions(blockNumber uint64) []*protos.Transaction {
	return []*protos.Transaction{&protos.Transaction{
		Payload: SimpleGetStateDelta(blockNumber),
	}}
}

func SimpleGetConsensusMetadata(blockNumber uint64) []byte {
	return []byte(fmt.Sprintf("ConsensusMetaData:%d", blockNumber))
}

func SimpleGetBlockHash(blockNumber uint64) []byte {
	if blockNumber == ^uint64(0) {
		// This occurs only when we are the genesis block
		return []byte("GenesisHash")
	}
	return SimpleHashBlock(&protos.Block{
		Transactions:      SimpleGetTransactions(blockNumber),
		ConsensusMetadata: SimpleGetConsensusMetadata(blockNumber),
		StateHash:         SimpleGetStateHash(blockNumber),
	})
}

func SimpleGetBlock(blockNumber uint64) *protos.Block {
	return &protos.Block{
		Transactions:      SimpleGetTransactions(blockNumber),
		ConsensusMetadata: SimpleGetConsensusMetadata(blockNumber),
		StateHash:         SimpleGetStateHash(blockNumber),
		PreviousBlockHash: SimpleGetBlockHash(blockNumber - 1),
	}
}

func TestMockLedger(t *testing.T) {
	remoteLedgers := make(map[uint64]consensus.ReadOnlyLedger)
	rl := &MockRemoteLedger{11}
	remoteLedgers[0] = rl

	ml := NewMockLedger(&remoteLedgers, nil)
	ml.GetCurrentStateHash()

	blockMessages, err := ml.GetRemoteBlocks(0, 10, 0)

	for blockMessage := range blockMessages {
		current := blockMessage.Range.Start
		i := 0
		for {
			_ = ml.PutBlock(current, blockMessage.Blocks[i]) // Never fails
			i++

			if current == blockMessage.Range.End {
				break
			}

			if blockMessage.Range.Start < blockMessage.Range.End {
				current++
			} else {
				current--
			}
		}
	}

	blockNumber, err := ml.VerifyBlockchain(10, 0)

	if nil != err {
		t.Fatalf("Retrieved blockchain did not validate at block %d with error '%s', error in mock ledger implementation.", blockNumber, err)
	}

	if blockNumber != 0 {
		t.Fatalf("Retrieved blockchain did not validate at block %d, error in mock ledger implementation.", blockNumber)
	}

	_ = ml.PutBlock(3, &protos.Block{ // Never fails
		PreviousBlockHash: []byte("WRONG"),
		StateHash:         []byte("WRONG"),
	})

	blockNumber, err = ml.VerifyBlockchain(10, 0)

	if blockNumber != 4 {
		t.Fatalf("Mangled blockchain did not detect the correct block with the wrong hash, error in mock ledger implementation.")
	}

	syncStateMessages, err := ml.GetRemoteStateSnapshot(0)

	if nil != err {
		t.Fatalf("Remote state snapshot call failed, error in mock ledger implementation: %s", err)
	}

	_ = ml.EmptyState() // Never fails
	for syncStateMessage := range syncStateMessages {
		if err := ml.ApplyStateDelta(syncStateMessage.Delta, false); err != nil {
			t.Fatalf("Error applying state delta : %s", err)
		}
	}

	block10, err := ml.GetBlock(10)

	if nil != err {
		t.Fatalf("Error retrieving block 10, which we should have, error in mock ledger implementation")
	}
	stateHash, _ := ml.GetCurrentStateHash()
	if !bytes.Equal(block10.StateHash, stateHash) {
		t.Fatalf("Ledger state hash %s and block state hash %s do not match, error in mock ledger implementation", stateHash, block10.StateHash)
	}
}
