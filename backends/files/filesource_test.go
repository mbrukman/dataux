package files_test

import (
	"bufio"
	"database/sql"
	"fmt"
	"io/ioutil"
	"testing"

	u "github.com/araddon/gou"
	"github.com/bmizerany/assert"
	"github.com/lytics/cloudstorage"
	"github.com/lytics/cloudstorage/logging"

	"github.com/araddon/qlbridge/plan"

	"github.com/dataux/dataux/frontends/mysqlfe/testmysql"
	"github.com/dataux/dataux/planner"
	tu "github.com/dataux/dataux/testutil"
)

/*

# to run Google Cloud tests you must have
# 1)   have run "gcloud auth login"
# 2)   set env variable "TESTINT=1"

export TESTINT=1


*/
var (
	testServicesRunning bool
)

const (
	DbName = "datauxtest"
)

var localconfig = &cloudstorage.CloudStoreContext{
	LogggingContext: "unittest",
	TokenSource:     cloudstorage.LocalFileSource,
	LocalFS:         "/tmp/mockcloud",
	TmpDir:          "/tmp/localcache",
}

var gcsIntconfig = &cloudstorage.CloudStoreContext{
	LogggingContext: "dataux-test",
	TokenSource:     cloudstorage.GCEDefaultOAuthToken,
	Project:         "lytics-dev",
	Bucket:          "lytics-dataux-tests",
	TmpDir:          "/tmp/localcache",
}

func init() {
	u.SetupLogging("debug")
	u.SetColorOutput()
	tu.Setup()
}

func jobMaker(ctx *plan.Context) (*planner.ExecutorGrid, error) {
	ctx.Schema = testmysql.Schema
	return planner.BuildSqlJob(ctx, testmysql.ServerCtx.Grid)
}

func RunTestServer(t testing.TB) {
	if !testServicesRunning {
		testServicesRunning = true
		planner.GridConf.JobMaker = jobMaker
		planner.GridConf.SchemaLoader = testmysql.SchemaLoader
		planner.GridConf.SupressRecover = testmysql.Conf.SupressRecover
		createTestData(t)
		testmysql.RunTestServer(t)
		quit := make(chan bool)
		planner.RunWorkerNodes(quit, 2, testmysql.ServerCtx.Reg)
	}
}

func createLocalStore() (cloudstorage.Store, error) {

	cloudstorage.LogConstructor = func(prefix string) logging.Logger {
		return logging.NewStdLogger(true, logging.DEBUG, prefix)
	}

	var config *cloudstorage.CloudStoreContext
	//os.RemoveAll("/tmp/mockcloud")
	//os.RemoveAll("/tmp/localcache")
	config = localconfig
	// else {
	// 	config = gcsIntconfig
	// }
	return cloudstorage.NewStore(config)
}

func clearStore(t *testing.T, store cloudstorage.Store) {
	q := cloudstorage.Query{}
	q.Sorted()
	objs, err := store.List(q)
	assert.T(t, err == nil)
	for _, o := range objs {
		u.Debugf("deleting %q", o.Name())
		store.Delete(o.Name())
	}

	// if os.Getenv("TESTINT") != "" {
	// 	// GCS is lazy about deletes...
	// 	time.Sleep(15 * time.Second)
	// }
}

func validateQuerySpec(t *testing.T, testSpec tu.QuerySpec) {
	RunTestServer(t)
	tu.ValidateQuerySpec(t, testSpec)
}

func createTestData(t testing.TB) {
	store, err := createLocalStore()
	if err != nil {
		t.Errorf("Could not create localstore %v", err)
	}
	//clearStore(t, store)
	//defer clearStore(t, store)

	//Create a new object and write to it.
	obj, err := store.NewObject("tables/article/article1.csv")
	if err != nil {
		return // already created
	}
	f, err := obj.Open(cloudstorage.ReadWrite)
	if t != nil {
		t.Errorf("Could not create article1.csv %v", err)
	}

	w := bufio.NewWriter(f)
	w.WriteString(tu.Articles[0].Header())
	w.WriteByte('\n')
	lastIdx := len(tu.Articles) - 1
	for i, a := range tu.Articles {
		w.WriteString(a.Row())
		if i != lastIdx {
			w.WriteByte('\n')
		}
	}
	w.Flush()
	err = obj.Close()
	if err != nil {
		t.Errorf("Could not close obj %v", err)
	}

	obj, _ = store.NewObject("tables/user/user1.csv")
	f, _ = obj.Open(cloudstorage.ReadWrite)
	w = bufio.NewWriter(f)
	w.WriteString(tu.Users[0].Header())
	w.WriteByte('\n')
	lastIdx = len(tu.Users) - 1
	for i, a := range tu.Users {
		w.WriteString(a.Row())
		if i != lastIdx {
			w.WriteByte('\n')
		}
	}
	w.Flush()
	obj.Close()

	//Read the object back out of the cloud storage.
	obj2, err := store.Get("tables/article/article1.csv")
	if err != nil {
		t.Errorf("Could not create article1.csv 2 %v", err)
	}

	f2, err := obj2.Open(cloudstorage.ReadOnly)
	if err != nil {
		t.Errorf("Could not open %v", err)
	}

	bytes, err := ioutil.ReadAll(f2)
	if err != nil {
		t.Errorf("Could not read all %v", err)
	}

	if tu.ArticleCsv != string(bytes) {
		t.Errorf("Expected equal bytes for csv")
	}
}

func TestShowTables(t *testing.T) {

	found := false
	data := struct {
		Table string `db:"Table"`
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "show tables;",
		ExpectRowCt: 3,
		ValidateRowData: func() {
			u.Infof("%v", data)
			assert.Tf(t, data.Table != "", "%v", data)
			if data.Table == "article" {
				found = true
			}
		},
		RowData: &data,
	})
	assert.Tf(t, found, "Must have found article table with show")
}

func TestSelectFilesList(t *testing.T) {
	data := struct {
		File      string
		Table     string
		Size      int
		Partition int
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select file, `table`, size, partition from localfiles_files",
		ExpectRowCt: 2,
		ValidateRowData: func() {
			u.Infof("%v", data)
			// assert.Tf(t, data.Deleted == false, "Not deleted? %v", data)
			// assert.Tf(t, data.Title == "article1", "%v", data)
		},
		RowData: &data,
	})
}

func TestSelectStar(t *testing.T) {
	RunTestServer(t)
	db, err := sql.Open("mysql", "root@tcp(127.0.0.1:13307)/datauxtest")
	assert.T(t, err == nil)
	rows, err := db.Query("select * from article;")
	assert.Tf(t, err == nil, "did not want err but got %v", err)
	cols, _ := rows.Columns()
	assert.Tf(t, len(cols) == 7, "want 7 cols but got %v", cols)
	assert.Tf(t, rows.Next(), "must get next row but couldn't")
	readCols := make([]interface{}, len(cols))
	writeCols := make([]string, len(cols))
	for i, _ := range writeCols {
		readCols[i] = &writeCols[i]
	}
	rows.Scan(readCols...)
	//assert.Tf(t, len(rows) == 7, "must get 7 rows but got %d", len(rows))
}

func TestSimpleRowSelect(t *testing.T) {
	data := struct {
		Title   string
		Count   int
		Deleted bool
		//Category *datasource.StringArray
	}{}
	validateQuerySpec(t, tu.QuerySpec{
		Sql:         "select title, count, deleted from article WHERE `author` = \"aaron\" ",
		ExpectRowCt: 1,
		ValidateRowData: func() {
			//u.Infof("%v", data)
			assert.Tf(t, data.Deleted == false, "Not deleted? %v", data)
			assert.Tf(t, data.Title == "article1", "%v", data)
		},
		RowData: &data,
	})
}

type player struct {
	PlayerId string
	YearId   string
	TeamId   string
}

// go test -bench="FileSqlWhere" --run="FileSqlWhere"
//
// go test -bench="FileSqlWhere" --run="FileSqlWhere" -cpuprofile cpu.out
// go tool pprof files.test cpu.out
func BenchmarkFileSqlWhere(b *testing.B) {

	RunTestServer(b)
	// This is a connection to RunTestServer, which starts on port 13307
	db, err := sql.Open("mysql", fmt.Sprintf("root@tcp(127.0.0.1:13307)/%s?parseTime=true", DbName))
	if err != nil {
		b.Errorf("Could not create localstore %v", err)
	}

	sqlText := `SELECT layerID, yearID, teamID 
	FROM appearances 
	WHERE playerID = "barnero01" AND yearID = "1871";
	`

	b.StartTimer()

	for i := 0; i < b.N; i++ {
		u.Debugf("starting iter? %d", b.N)
		rows, err := db.Query(sqlText)
		if err != nil {
			b.Errorf("Could not query %v", err)
		}
		players := make([]player, 0)
		for rows.Next() {
			var p player
			rows.Scan(&p.PlayerId, &p.YearId, &p.TeamId)
			players = append(players, p)
		}
		rows.Close()
		if len(players) != 1 {
			b.Errorf("Could not find players")
		}
	}
}

/*
                                         356800349
BenchmarkFileSqlWhere-4   	       1	1435390817 ns/op
ok  	github.com/dataux/dataux/backends/files	1.453s

*/
