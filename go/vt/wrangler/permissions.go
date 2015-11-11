// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wrangler

import (
	"fmt"
	"sort"
	"sync"

	log "github.com/golang/glog"
	"github.com/youtube/vitess/go/vt/concurrency"
	"github.com/youtube/vitess/go/vt/mysqlctl/tmutils"
	"github.com/youtube/vitess/go/vt/topo/topoproto"
	"golang.org/x/net/context"

	tabletmanagerdatapb "github.com/youtube/vitess/go/vt/proto/tabletmanagerdata"
	pb "github.com/youtube/vitess/go/vt/proto/topodata"
)

// GetPermissions returns the permissions set on a remote tablet
func (wr *Wrangler) GetPermissions(ctx context.Context, tabletAlias *pb.TabletAlias) (*tabletmanagerdatapb.Permissions, error) {
	tablet, err := wr.ts.GetTablet(ctx, tabletAlias)
	if err != nil {
		return nil, err
	}

	return wr.tmc.GetPermissions(ctx, tablet)
}

// diffPermissions is a helper method to asynchronously diff a permissions
func (wr *Wrangler) diffPermissions(ctx context.Context, masterPermissions *tabletmanagerdatapb.Permissions, masterAlias *pb.TabletAlias, alias *pb.TabletAlias, wg *sync.WaitGroup, er concurrency.ErrorRecorder) {
	defer wg.Done()
	log.Infof("Gathering permissions for %v", topoproto.TabletAliasString(alias))
	slavePermissions, err := wr.GetPermissions(ctx, alias)
	if err != nil {
		er.RecordError(err)
		return
	}

	log.Infof("Diffing permissions for %v", topoproto.TabletAliasString(alias))
	tmutils.DiffPermissions(topoproto.TabletAliasString(masterAlias), masterPermissions, topoproto.TabletAliasString(alias), slavePermissions, er)
}

// ValidatePermissionsShard validates all the permissions are the same
// in a shard
func (wr *Wrangler) ValidatePermissionsShard(ctx context.Context, keyspace, shard string) error {
	si, err := wr.ts.GetShard(ctx, keyspace, shard)
	if err != nil {
		return err
	}

	// get permissions from the master, or error
	if !si.HasMaster() {
		return fmt.Errorf("No master in shard %v/%v", keyspace, shard)
	}
	log.Infof("Gathering permissions for master %v", topoproto.TabletAliasString(si.MasterAlias))
	masterPermissions, err := wr.GetPermissions(ctx, si.MasterAlias)
	if err != nil {
		return err
	}

	// read all the aliases in the shard, that is all tablets that are
	// replicating from the master
	aliases, err := wr.ts.FindAllTabletAliasesInShard(ctx, keyspace, shard)
	if err != nil {
		return err
	}

	// then diff all of them, except master
	er := concurrency.AllErrorRecorder{}
	wg := sync.WaitGroup{}
	for _, alias := range aliases {
		if topoproto.TabletAliasEqual(alias, si.MasterAlias) {
			continue
		}
		wg.Add(1)
		go wr.diffPermissions(ctx, masterPermissions, si.MasterAlias, alias, &wg, &er)
	}
	wg.Wait()
	if er.HasErrors() {
		return fmt.Errorf("Permissions diffs:\n%v", er.Error().Error())
	}
	return nil
}

// ValidatePermissionsKeyspace validates all the permissions are the same
// in a keyspace
func (wr *Wrangler) ValidatePermissionsKeyspace(ctx context.Context, keyspace string) error {
	// find all the shards
	shards, err := wr.ts.GetShardNames(ctx, keyspace)
	if err != nil {
		return err
	}

	// corner cases
	if len(shards) == 0 {
		return fmt.Errorf("No shards in keyspace %v", keyspace)
	}
	sort.Strings(shards)
	if len(shards) == 1 {
		return wr.ValidatePermissionsShard(ctx, keyspace, shards[0])
	}

	// find the reference permissions using the first shard's master
	si, err := wr.ts.GetShard(ctx, keyspace, shards[0])
	if err != nil {
		return err
	}
	if !si.HasMaster() {
		return fmt.Errorf("No master in shard %v/%v", keyspace, shards[0])
	}
	referenceAlias := si.MasterAlias
	log.Infof("Gathering permissions for reference master %v", topoproto.TabletAliasString(referenceAlias))
	referencePermissions, err := wr.GetPermissions(ctx, si.MasterAlias)
	if err != nil {
		return err
	}

	// then diff with all tablets but master 0
	er := concurrency.AllErrorRecorder{}
	wg := sync.WaitGroup{}
	for _, shard := range shards {
		aliases, err := wr.ts.FindAllTabletAliasesInShard(ctx, keyspace, shard)
		if err != nil {
			er.RecordError(err)
			continue
		}

		for _, alias := range aliases {
			if topoproto.TabletAliasEqual(alias, si.MasterAlias) {
				continue
			}

			wg.Add(1)
			go wr.diffPermissions(ctx, referencePermissions, referenceAlias, alias, &wg, &er)
		}
	}
	wg.Wait()
	if er.HasErrors() {
		return fmt.Errorf("Permissions diffs:\n%v", er.Error().Error())
	}
	return nil
}
