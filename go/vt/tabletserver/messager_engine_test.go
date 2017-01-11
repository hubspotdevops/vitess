// Copyright 2017, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tabletserver

import (
	"reflect"
	"testing"
	"time"

	querypb "github.com/youtube/vitess/go/vt/proto/query"
	topodatapb "github.com/youtube/vitess/go/vt/proto/topodata"
	"github.com/youtube/vitess/go/vt/schema"
)

func TestMessagerEngineState(t *testing.T) {
	db := setUpTabletServerTest()
	testUtils := newTestUtils()
	config := testUtils.newQueryServiceConfig()
	config.TransactionCap = 1
	tsv := NewTabletServer(config)
	dbconfigs := testUtils.newDBConfigs(db)
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	err := tsv.StartService(target, dbconfigs, testUtils.newMysqld(&dbconfigs))
	if err != nil {
		t.Fatalf("StartService failed: %v", err)
	}
	defer tsv.StopService()

	me := NewMessagerEngine(tsv, config, tsv.queryServiceStats)
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	if l := len(tsv.qe.schemaInfo.notifiers); l != 1 {
		t.Errorf("len(notifiers): %d, want 1", l)
	}
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	if l := len(tsv.qe.schemaInfo.notifiers); l != 1 {
		t.Errorf("len(notifiers) after reopen: %d, want 1", l)
	}

	me.Close()
	if l := len(tsv.qe.schemaInfo.notifiers); l != 0 {
		t.Errorf("len(notifiers) after close: %d, want 0", l)
	}

	me.Close()
	if l := len(tsv.qe.schemaInfo.notifiers); l != 0 {
		t.Errorf("len(notifiers) after close: %d, want 0", l)
	}
}

func TestMessagerEngineSchemaChanged(t *testing.T) {
	db := setUpTabletServerTest()
	testUtils := newTestUtils()
	config := testUtils.newQueryServiceConfig()
	config.TransactionCap = 1
	tsv := NewTabletServer(config)
	dbconfigs := testUtils.newDBConfigs(db)
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	err := tsv.StartService(target, dbconfigs, testUtils.newMysqld(&dbconfigs))
	if err != nil {
		t.Fatalf("StartService failed: %v", err)
	}
	defer tsv.StopService()

	me := NewMessagerEngine(tsv, config, tsv.queryServiceStats)
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	tables := map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
		"t2": {
			Type: schema.NoType,
		},
	}
	me.schemaChanged(tables)
	got := extractManagerNames(me.managers)
	want := map[string]bool{"msg": true, "t1": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got: %+v, want %+v", got, want)
	}
	tables = map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
		"t2": {
			Type: schema.NoType,
		},
		"t3": {
			Type: schema.Message,
		},
	}
	me.schemaChanged(tables)
	got = extractManagerNames(me.managers)
	want = map[string]bool{"msg": true, "t1": true, "t3": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got: %+v, want %+v", got, want)
	}
	tables = map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
		"t2": {
			Type: schema.NoType,
		},
		"t4": {
			Type: schema.Message,
		},
	}
	me.schemaChanged(tables)
	got = extractManagerNames(me.managers)
	// schemaChanged is only additive.
	want = map[string]bool{"msg": true, "t1": true, "t3": true, "t4": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got: %+v, want %+v", got, want)
	}
}

func extractManagerNames(in map[string]*MessageManager) map[string]bool {
	out := make(map[string]bool)
	for k := range in {
		out[k] = true
	}
	return out
}

func TestSubscribe(t *testing.T) {
	db := setUpTabletServerTest()
	testUtils := newTestUtils()
	config := testUtils.newQueryServiceConfig()
	config.TransactionCap = 1
	tsv := NewTabletServer(config)
	dbconfigs := testUtils.newDBConfigs(db)
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	err := tsv.StartService(target, dbconfigs, testUtils.newMysqld(&dbconfigs))
	if err != nil {
		t.Fatalf("StartService failed: %v", err)
	}
	defer tsv.StopService()

	me := NewMessagerEngine(tsv, config, tsv.queryServiceStats)
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	tables := map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
		"t2": {
			Type: schema.Message,
		},
	}
	me.schemaChanged(tables)
	r1 := newTestReceiver(1)
	r2 := newTestReceiver(1)
	// Each receiver is subscribed to different managers.
	me.Subscribe("t1", r1)
	me.Subscribe("t2", r2)
	row1 := &MessageRow{
		id: "1",
	}
	row2 := &MessageRow{
		id: "2",
	}
	me.managers["t1"].Add(row1)
	me.managers["t2"].Add(row2)
	<-r1.ch
	<-r2.ch
	// One receiver subscribed to multiple managers.
	me.Unsubscribe(r1)
	me.Subscribe("t1", r2)
	me.managers["t2"].Add(row1)
	me.managers["t2"].Add(row2)
	<-r2.ch
	<-r2.ch

	// Error case.
	want := "message table t3 not found"
	err = me.Subscribe("t3", r1)
	if err == nil || err.Error() != want {
		t.Errorf("Subscribe: %v, want %s", err, want)
	}
}

func TestLockDB(t *testing.T) {
	db := setUpTabletServerTest()
	testUtils := newTestUtils()
	config := testUtils.newQueryServiceConfig()
	config.TransactionCap = 1
	tsv := NewTabletServer(config)
	dbconfigs := testUtils.newDBConfigs(db)
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	err := tsv.StartService(target, dbconfigs, testUtils.newMysqld(&dbconfigs))
	if err != nil {
		t.Fatalf("StartService failed: %v", err)
	}
	defer tsv.StopService()

	me := NewMessagerEngine(tsv, config, tsv.queryServiceStats)
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	tables := map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
		"t2": {
			Type: schema.Message,
		},
	}
	me.schemaChanged(tables)
	r1 := newTestReceiver(0)
	me.Subscribe("t1", r1)

	row1 := &MessageRow{
		id: "1",
	}
	row2 := &MessageRow{
		TimeNext: time.Now().UnixNano() + int64(10*time.Minute),
		id:       "2",
	}
	newMessages := map[string][]*MessageRow{"t1": {row1, row2}, "t3": {row1}}
	unlock := me.LockDB(newMessages, nil)
	me.UpdateCaches(newMessages, nil)
	unlock()
	<-r1.ch
	time.Sleep(10 * time.Microsecond)
	// row2 should not be sent.
	select {
	case mr := <-r1.ch:
		t.Errorf("Unexpected message: %v", mr)
	default:
	}

	r2 := newTestReceiver(0)
	me.Subscribe("t2", r2)
	mm := me.managers["t2"]
	mm.Add(&MessageRow{id: "1"})
	// Make sure the first message is enqueued.
	r2.WaitForCount(1)
	// "2" will be in the cache.
	mm.Add(&MessageRow{id: "2"})
	changedMessages := map[string][]string{"t2": {"2"}, "t3": {"2"}}
	unlock = me.LockDB(nil, changedMessages)
	// This should delete "2".
	me.UpdateCaches(nil, changedMessages)
	unlock()
	<-r2.ch
	time.Sleep(10 * time.Microsecond)
	// There should be no more messages.
	select {
	case mr := <-r2.ch:
		t.Errorf("Unexpected message: %v", mr)
	default:
	}
}

func TestMEGenerate(t *testing.T) {
	db := setUpTabletServerTest()
	testUtils := newTestUtils()
	config := testUtils.newQueryServiceConfig()
	config.TransactionCap = 1
	tsv := NewTabletServer(config)
	dbconfigs := testUtils.newDBConfigs(db)
	target := querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	err := tsv.StartService(target, dbconfigs, testUtils.newMysqld(&dbconfigs))
	if err != nil {
		t.Fatalf("StartService failed: %v", err)
	}
	defer tsv.StopService()

	me := NewMessagerEngine(tsv, config, tsv.queryServiceStats)
	if err := me.Open(dbconfigs); err != nil {
		t.Fatal(err)
	}
	me.schemaChanged(map[string]*schema.Table{
		"t1": {
			Type: schema.Message,
		},
	})
	if _, _, err := me.GenerateAckQuery("t1", []string{"1"}); err != nil {
		t.Error(err)
	}
	want := "message table t2 not found in schema"
	if _, _, err := me.GenerateAckQuery("t2", []string{"1"}); err == nil || err.Error() != want {
		t.Errorf("me.GenerateAckQuery(invalid): %v, want %s", err, want)
	}

	if _, _, err := me.GenerateRescheduleQuery("t1", []string{"1"}, 0); err != nil {
		t.Error(err)
	}
	if _, _, err := me.GenerateRescheduleQuery("t2", []string{"1"}, 0); err == nil || err.Error() != want {
		t.Errorf("me.GenerateAckQuery(invalid): %v, want %s", err, want)
	}
}