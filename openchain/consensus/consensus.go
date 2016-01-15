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

package consensus

import pb "github.com/openblockchain/obc-peer/protos"

// Consenter is implemented by every consensus plugin package
type Consenter interface {
	RecvMsg(msg *pb.OpenchainMessage) error
}

// ReadOnlyLedger is used for interrogating the blockchain
type ReadOnlyLedger interface {
	GetBlock(id uint64) (block *pb.Block, err error)
	GetCurrentStateHash() (stateHash []byte, err error)
	GetBlockchainSize() (uint64, error)
}

// UtilLedger contains additional useful utility functions for interrogating the blockchain
type UtilLedger interface {
	HashBlock(block *pb.Block) ([]byte, error)
	VerifyBlockchain(start, finish uint64) (uint64, error)
}

// WritableLedger is useful for updating the blockchain during state transfer
type WritableLedger interface {
	PutBlock(blockNumber uint64, block *pb.Block) error
	ApplyStateDelta(delta []byte, unapply bool) error
	EmptyState() error
}

// Ledger is an unrestricted union of reads, utilities, and updates
type Ledger interface {
	ReadOnlyLedger
	UtilLedger
	WritableLedger
}

// Executor is used to invoke transactions, potentially modifying the backing ledger
type Executor interface {
	BeginTxBatch(id interface{}) error
	ExecTXs(txs []*pb.Transaction) ([]byte, []error)
	CommitTxBatch(id interface{}, transactions []*pb.Transaction, transactionsResults []*pb.TransactionResult, metadata []byte) error
	RollbackTxBatch(id interface{}) error
	PreviewCommitTxBatchBlock(id interface{}, transactions []*pb.Transaction, metadata []byte) (*pb.Block, error)
}

// RemoteLedgers is used to interrogate the blockchain of other replicas
type RemoteLedgers interface {
	GetRemoteBlocks(replicaId uint64, start, finish uint64) (<-chan *pb.SyncBlocks, error)
	GetRemoteStateSnapshot(replicaId uint64) (<-chan *pb.SyncStateSnapshot, error)
	GetRemoteStateDeltas(replicaId uint64, start, finish uint64) (<-chan *pb.SyncStateDeltas, error)
}

// BlockchainPackage serves as interface to the blockchain oriented activities, such as executing transactions, querying, and updating the ledger
type BlockchainPackage interface {
	Executor
	Ledger
	RemoteLedgers
}

// CPI (Consensus Programming Interface) is the set of
// stack-facing methods available to the consensus plugin
type CPI interface {
	GetNetworkHandles() (self string, network []string, err error)
	GetReplicaHandle(id uint64) (handle string, err error)
	GetReplicaID(handle string) (id uint64, err error)

	Broadcast(msg *pb.OpenchainMessage) error
	Unicast(msg *pb.OpenchainMessage, receiverHandle string) error

	BlockchainPackage
}
