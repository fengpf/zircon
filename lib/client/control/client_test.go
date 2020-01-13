package control

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"
	"log"

	"zircon/lib/apis"
	"zircon/lib/chunkserver"
	"zircon/lib/etcd"
	"zircon/lib/frontend"
	"zircon/lib/rpc"
	"zircon/lib/util"
	"zircon/lib/metadatacache"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Prepares three chunkservers (cs0-cs2) and one frontend server (fe0)
func PrepareLocalCluster(t *testing.T) (rpccache rpc.ConnectionCache, stats chunkserver.StorageStats, fe apis.Frontend, teardown func()) {
	cache := &rpc.MockCache{
		Frontends: map[apis.ServerAddress]apis.Frontend{},
		Chunkservers: map[apis.ServerAddress]apis.Chunkserver{},
	}
	etcds, teardown1 := etcd.PrepareSubscribeForTesting(t)
	var teardowns util.MultiTeardown
	teardowns.Add(teardown1)
	var allStats []chunkserver.StorageStats
	for i := 0; i < 3; i++ {
		name := apis.ServerName(fmt.Sprintf("cs%d", i))
		address := apis.ServerAddress(fmt.Sprintf("cs-address-%d", i))

		cs, csStats, csTeardown := chunkserver.NewTestChunkserver(t, cache)
		teardowns.Add(csTeardown)
		allStats = append(allStats, csStats)
		cache.Chunkservers[address] = cs

		etcd0, etcdClientTeardown := etcds(name)
		assert.NoError(t, etcd0.UpdateAddress(address, apis.CHUNKSERVER))
		teardowns.Add(etcdClientTeardown)
	}

	etcd0, teardown2 := etcds("fe0")
	teardowns.Add(teardown2)
	fe, err := frontend.ConstructFrontend(etcd0, cache)
	assert.NoError(t, err)
	mdc0, err := metadatacache.NewCache(cache, etcd0)
	assert.NoError(t, err)
	cache.MetadataCaches = map[apis.ServerAddress]apis.MetadataCache{
		"mdc-address-0": mdc0,
	}
	assert.NoError(t, etcd0.UpdateAddress("mdc-address-0", apis.METADATACACHE))

	return cache, func() int {
			// TODO: include partial metadata usage in these stats?
			sum := 0
			for _, statf := range allStats {
				sum += statf()
			}
			return sum
		}, fe, teardowns.Teardown
}

func PrepareSimpleClient(t *testing.T) (apis.Client, func()) {
	cache, _, fe, teardown := PrepareLocalCluster(t)
	client, err := ConstructClient(fe, cache)
	require.NoError(t, err)
	return client, func() {
		client.Close()
		teardown()
	}
}

// Tests the ability for a single client to properly interact with a cluster, and
// perform a simple series of new, read, write, and delete operations, including
// correct error handling.
func TestSimpleClientReadWrite(t *testing.T) {
	client, teardown := PrepareSimpleClient(t)
	defer teardown()

	cn, err := client.New()
	require.NoError(t, err)

	data, ver, err := client.Read(cn, 0, 1)
	assert.NoError(t, err)
	assert.Equal(t, apis.Version(0), ver)
	assert.Equal(t, []byte{0}, data)

	ver, err = client.Write(cn, 0, apis.AnyVersion, []byte("hello, world!"))
	require.NoError(t, err)
	assert.True(t, ver > 0)

	data, ver2, err := client.Read(cn, 0, apis.MaxChunkSize)
	assert.NoError(t, err)
	assert.Equal(t, ver, ver2)
	assert.Equal(t, "hello, world!", string(util.StripTrailingZeroes(data)))

	ver3, err := client.Write(cn, 7, ver2, []byte("home!"))
	assert.NoError(t, err)
	assert.True(t, ver3 > ver2)

	ver5, err := client.Write(cn, 7, ver2, []byte("earth..."))
	assert.Error(t, err)
	assert.Equal(t, ver3, ver5) // make sure it returns the correct new version after staleness failure

	data, ver4, err := client.Read(cn, 0, apis.MaxChunkSize)
	assert.NoError(t, err)
	assert.Equal(t, ver3, ver4)
	assert.Equal(t, "hello, home!!", string(util.StripTrailingZeroes(data)))

	assert.Error(t, client.Delete(cn, ver2))

	data, ver6, err := client.Read(cn, 0, apis.MaxChunkSize)
	assert.NoError(t, err)
	assert.Equal(t, ver4, ver6)
	assert.Equal(t, "hello, home!!", string(util.StripTrailingZeroes(data)))

	assert.NoError(t, client.Delete(cn, ver6))

	_, _, err = client.Read(cn, 0, apis.MaxChunkSize)
	assert.Error(t, err)
}

// Tests that error checking works properly for reads and writes that exceed the maximum chunk size
func TestMaxSizeChecking(t *testing.T) {
	client, teardown := PrepareSimpleClient(t)
	defer teardown()

	cn, err := client.New()
	assert.NoError(t, err)

	data := make([]byte, apis.MaxChunkSize-1)
	data[len(data)-1] = 'a'
	ver, err := client.Write(cn, 2, apis.AnyVersion, data)
	assert.Error(t, err)
	assert.Equal(t, apis.Version(0), ver)

	// make sure that the failed write didn't actually succeed
	rdata, ver, err := client.Read(cn, 2, 5)
	assert.NoError(t, err)
	assert.Equal(t, apis.Version(0), ver)
	assert.Equal(t, []byte{0,0,0,0,0}, rdata)

	ver, err = client.Write(cn, 1, apis.AnyVersion, data)
	assert.NoError(t, err)
	assert.True(t, ver > 0)

	// confirm write succeeded this time
	rdata, ver2, err := client.Read(cn, 0, apis.MaxChunkSize)
	require.NoError(t, err)
	assert.Equal(t, ver, ver2)
	assert.Equal(t, apis.MaxChunkSize, len(rdata))
	assert.Equal(t, byte('a'), rdata[apis.MaxChunkSize-1])
	assert.Empty(t, util.StripTrailingZeroes(rdata[:apis.MaxChunkSize-1]))

	// attempt out-of-bounds read
	_, _, err = client.Read(cn, 1, apis.MaxChunkSize)
	assert.Error(t, err)
}

func TestReadRate(t *testing.T) {
	cache, _, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	var chunk apis.ChunkNum
	var xver apis.Version

	func() {
		setupClient, err := ConstructClient(fe, cache)
		require.NoError(t, err)
		defer setupClient.Close()
		chunk, err = setupClient.New()
		assert.NoError(t, err)
		xver, err = setupClient.Write(chunk, 0, apis.AnyVersion, []byte("hello world"))
		assert.NoError(t, err)
	}()

	complete := make(chan int)
	count := 10

	finishAt := time.Now().Add(time.Second * 5)
	for i := 0; i < count; i++ {
		go func(clientId int) {
			subcount := 0
			ok := false
			defer func() {
				if ok {
					complete <- subcount
				} else {
					complete <- -1
				}
			}()

			client, err := ConstructClient(fe, cache)
			assert.NoError(t, err)
			defer client.Close()

			for time.Now().Before(finishAt) {
				data, ver, err := client.Read(chunk, 0, 128)
				assert.NoError(t, err)
				assert.Equal(t, xver, ver)
				assert.Equal(t, "hello world", string(util.StripTrailingZeroes(data)))

				subcount++
			}

			ok = true
		}(i)
	}

	finalCount := 0
	for i := 0; i < count; i++ {
		subtotal := <-complete
		assert.True(t, subtotal >= 300, "not enough requests processed: %d/300", subtotal)
		assert.NotEqual(t, 0, subtotal)
		finalCount += subtotal
	}
	// should be able to process at least four contended requests per second on average
	assert.True(t, finalCount >= 3000, "not enough requests processed: %d/3000", finalCount)

	log.Printf("results of read test: %d final\n", finalCount)
}

// Tests the ability for multiple clients to safely clobber each others' changes to a shared block of data.
func TestConflictingClients(t *testing.T) {
	cache, _, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	var chunk apis.ChunkNum

	func() {
		setupClient, err := ConstructClient(fe, cache)
		require.NoError(t, err)
		defer setupClient.Close()
		chunk, err = setupClient.New()
		assert.NoError(t, err)
		_, err = setupClient.Write(chunk, 0, apis.AnyVersion, []byte("0"))
		assert.NoError(t, err)
	}()

	complete := make(chan struct {
		subtotal int
		count    int
	})
	count := 10

	finishAt := time.Now().Add(time.Second * 5)
	for i := 0; i < count; i++ {
		go func(clientId int) {
			subtotal := 0
			subcount := 0
			ok := false
			defer func() {
				if ok {
					complete <- struct {
						subtotal int
						count    int
					}{subtotal, subcount}
				} else {
					complete <- struct {
						subtotal int
						count    int
					}{0, -1}
				}
			}()

			client, err := ConstructClient(fe, cache)
			assert.NoError(t, err)
			defer client.Close()

			for time.Now().Before(finishAt) {
				nextAddition := rand.Intn(10000) - 100
				subtotal += nextAddition

				for {
					num, ver, err := client.Read(chunk, 0, 128)
					assert.NoError(t, err)
					numnum, err := strconv.Atoi(string(util.StripTrailingZeroes(num)))
					newValue := nextAddition + numnum

					newData := make([]byte, 128)
					copy(newData, []byte(strconv.Itoa(newValue)))
					newver, err := client.Write(chunk, 0, ver, newData)
					if err == nil {
						assert.True(t, newver > ver)
						break
					}
					assert.True(t, newver >= ver || newver == 0)
				}

				subcount++
			}

			ok = true
		}(i)
	}

	finalSum := 0
	finalCount := 0
	for i := 0; i < count; i++ {
		subtotal := <-complete
		assert.True(t, subtotal.count >= 1, "not enough requests processed: %d/1", subtotal.count)
		assert.NotEqual(t, 0, subtotal.subtotal)
		finalCount += subtotal.count
		finalSum += subtotal.subtotal
	}
	// should be able to process at least four contended requests per second on average
	assert.True(t, finalCount >= 15, "not enough requests processed: %d/15", finalCount)

	log.Printf("results of conflicting test: %d final\n", finalCount)

	checkSum := func() int {
		teardownClient, err := ConstructClient(fe, cache)
		assert.NoError(t, err)
		defer teardownClient.Close()
		contents, _, err := teardownClient.Read(chunk, 0, 128)
		assert.NoError(t, err)
		result, err := strconv.Atoi(string(util.StripTrailingZeroes(contents)))
		assert.NoError(t, err)
		return result
	}

	assert.Equal(t, finalSum, checkSum())
}

// Tests the ability of many parallel clients to independently perform lots of operations on their own blocks.
func TestParallelClients(t *testing.T) {
	cache, _, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	complete := make(chan int)
	count := 10

	finishAt := time.Now().Add(time.Second * 5)
	for i := 0; i < count; i++ {
		go func(clientId int) {
			operations := 0
			ok := false
			defer func() {
				if ok {
					complete <- operations
				} else {
					complete <- -1
				}
			}()

			client, err := ConstructClient(fe, cache)
			require.NoError(t, err)
			defer client.Close()

			chunk, err := client.New()
			assert.NoError(t, err)

			lastVer, err := client.Write(chunk, 0, apis.AnyVersion, []byte("0"))
			assert.NoError(t, err)
			assert.True(t, lastVer > 0)

			total := 0

			for time.Now().Before(finishAt) {
				nextAddition := rand.Intn(10000) - 100
				total += nextAddition

				num, ver, err := client.Read(chunk, 0, 128)
				assert.NoError(t, err)
				assert.Equal(t, lastVer, ver)
				numnum, err := strconv.Atoi(string(util.StripTrailingZeroes(num)))
				newValue := nextAddition + numnum

				newData := make([]byte, 128)
				copy(newData, []byte(strconv.Itoa(newValue)))
				newver, err := client.Write(chunk, 0, ver, newData)
				assert.NoError(t, err)
				assert.True(t, newver > ver)

				lastVer = newver

				operations++
			}

			num, ver, err := client.Read(chunk, 0, 128)
			assert.NoError(t, err)
			assert.Equal(t, lastVer, ver)
			numnum, err := strconv.Atoi(string(util.StripTrailingZeroes(num)))

			assert.Equal(t, total, numnum)

			ok = true
		}(i)
	}

	ops := 0
	for i := 0; i < count; i++ {
		opsSingle := <-complete
		assert.True(t, opsSingle >= 15, "not enough requests processed: %d/15", opsSingle)
		ops += opsSingle
	}
	assert.True(t, ops >= 200, "not enough requests processed: %d/200", ops)

	log.Printf("results of conflicting test: %d final\n", ops)
}

// Tests the ability for deleted chunks to be fully cleaned up
func TestDeletion(t *testing.T) {
	cache, usage, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	client, err := ConstructClient(fe, cache)
	require.NoError(t, err)
	defer client.Close()

	// perform one creation and deletion so that any metadata needed is allocated

	chunk, err := client.New()
	assert.NoError(t, err)

	ver, err := client.Write(chunk, 0, apis.AnyVersion, []byte("hello"))
	assert.NoError(t, err)

	assert.NoError(t, client.Delete(chunk, ver))

	// now we sample the data usage, and launch into a whole bunch of creation and deletion

	initial := usage()

	pass := make(chan bool)
	count := 5

	for i := 0; i < count; i++ {
		go func() {
			ok := false
			defer func() {
				pass <- ok
			}()

			for j := 0; j < 5; j++ {
				chunk, err := client.New()
				assert.NoError(t, err)

				ver, err := client.Write(chunk, 0, apis.AnyVersion, []byte("hello"))
				assert.NoError(t, err)

				assert.NoError(t, client.Delete(chunk, ver))
			}

			ok = true
		}()
	}

	for i := 0; i < count; i++ {
		assert.True(t, <-pass)
	}

	// and after all of that is done, we shouldn't be using any more storage space

	final := usage()
	assert.Equal(t, initial, final)
}

// Tests the ability of old versions of chunks to be fully cleaned up
func TestCleanup(t *testing.T) {
	cache, usage, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	client, err := ConstructClient(fe, cache)
	require.NoError(t, err)
	defer client.Close()

	chunk, err := client.New()
	assert.NoError(t, err)

	ver, err := client.Write(chunk, 0, apis.AnyVersion, []byte("begin;"))
	offset := uint32(len("begin;"))
	assert.NoError(t, err)

	initial := usage()

	for i := 0; i < 25; i++ {
		entry := fmt.Sprintf("entry %d;", i)
		newver, err := client.Write(chunk, offset, ver, []byte(entry))
		assert.NoError(t, err)
		offset += uint32(len(entry))
		ver = newver
	}

	final := usage()

	assert.Equal(t, initial, final)

	// some extra checks that the data was all written and read back correctly

	data, version, err := client.Read(chunk, 0, 1000)
	assert.NoError(t, err)
	assert.Equal(t, ver, version)
	assert.Equal(t, "begin;", string(data[:6]))
	data = data[6:]
	for i := 0; i < 25; i++ {
		expected := fmt.Sprintf("entry %d;", i)
		assert.Equal(t, expected, string(data[:len(expected)]))
		data = data[len(expected):]
	}
	assert.Empty(t, util.StripTrailingZeroes(data))
}

// Tests the ability of a series of clients to invoke New() and then close their connections, and have all of the extra
// new chunks be safely cleaned up.
func TestIncompleteRemoval(t *testing.T) {
	t.Skip("NOT YET IMPLEMENTED")  // TODO: implement incomplete removal!

	cache, usage, fe, teardown := PrepareLocalCluster(t)
	defer teardown()

	// perform one creation and deletion so that any metadata needed is allocated
	func() {
		client, err := ConstructClient(fe, cache)
		require.NoError(t, err)
		defer client.Close()

		chunk, err := client.New()
		assert.NoError(t, err)

		ver, err := client.Write(chunk, 0, apis.AnyVersion, []byte("hello"))
		assert.NoError(t, err)

		assert.NoError(t, client.Delete(chunk, ver))
	}()

	count := 5
	initial := usage()
	chunknums := make(chan apis.ChunkNum, 100)
	done := make(chan bool)

	go func() {
		ok := false
		defer func() {
			done <- ok
		}()
		everything := map[apis.ChunkNum]bool{}
		duplicate := false
		for chunknum := range chunknums {
			if everything[chunknum] {
				duplicate = true
			}
			everything[chunknum] = true
		}
		// must have been at least one duplicate, which signifies reuse of chunk numbers... which we want!
		assert.True(t, duplicate)
		ok = true
	}()

	for i := 0; i < count; i++ {
		go func() {
			ok := false
			defer func() {
				done <- ok
			}()

			client, err := ConstructClient(fe, cache)
			assert.NoError(t, err)
			defer client.Close()

			for j := 0; j < 10; j++ {
				chunk, err := client.New()
				assert.NoError(t, err)
				chunknums <- chunk
			}
			ok = true
		}()
	}

	for i := 0; i < count; i++ {
		assert.True(t, <-done)
	}

	close(chunknums)

	assert.True(t, <-done)

	// all of the clients have been closed, so we should be back to the original data usage
	assert.Equal(t, initial, usage())
}
