package plakard

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/PlakarLabs/plakar/logger"
	"github.com/PlakarLabs/plakar/network"
	"github.com/PlakarLabs/plakar/repository"
	"github.com/PlakarLabs/plakar/storage"
	"github.com/google/uuid"
)

type ServerOptions struct {
	NoOpen   bool
	NoCreate bool
	NoDelete bool
}

func Server(repo *repository.Repository, addr string, options *ServerOptions) {

	network.ProtocolRegister()

	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go handleConnection(repo, c, c, options)
	}
}

func Stdio(options *ServerOptions) error {
	network.ProtocolRegister()

	handleConnection(nil, os.Stdin, os.Stdout, options)
	return nil
}

func handleConnection(repo *repository.Repository, rd io.Reader, wr io.Writer, options *ServerOptions) {
	var lrepository *repository.Repository

	lrepository = repo

	decoder := gob.NewDecoder(rd)
	encoder := gob.NewEncoder(wr)

	var wg sync.WaitGroup
	Uuid, _ := uuid.NewRandom()
	clientUuid := Uuid.String()

	homeDir := os.Getenv("HOME")

	for {
		request := network.Request{}
		err := decoder.Decode(&request)
		if err != nil {
			break
		}

		switch request.Type {
		case "ReqCreate":
			wg.Add(1)
			go func() {
				defer wg.Done()

				dirPath := request.Payload.(network.ReqCreate).Repository
				if dirPath == "" {
					dirPath = filepath.Join(homeDir, ".plakar")
				}

				logger.Trace("server", "%s: Create(%s, %s)", clientUuid, dirPath, request.Payload.(network.ReqCreate).Configuration)
				st, err := storage.Create(dirPath, request.Payload.(network.ReqCreate).Configuration)
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				lrepository, err = repository.New(st, nil)
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid:    request.Uuid,
					Type:    "ResCreate",
					Payload: network.ResCreate{Err: retErr},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqOpen":
			wg.Add(1)
			go func() {
				defer wg.Done()

				logger.Trace("server", "%s: Open()", clientUuid)

				location := request.Payload.(network.ReqOpen).Repository
				st, err := storage.Open(location)
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				lrepository, err = repository.New(st, nil)
				if err != nil {
					retErr = err.Error()
				}
				var payload network.ResOpen
				if err != nil {
					payload = network.ResOpen{Configuration: nil, Err: retErr}
				} else {
					config := repo.Configuration()
					payload = network.ResOpen{Configuration: &config, Err: retErr}
				}

				result := network.Request{
					Uuid:    request.Uuid,
					Type:    "ResOpen",
					Payload: payload,
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqClose":
			wg.Add(1)
			go func() {
				defer wg.Done()

				logger.Trace("server", "%s: Close()", clientUuid)

				var err error
				if repo == nil {
					err = lrepository.Close()
				}
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}

				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResClose",
					Payload: network.ResClose{
						Err: retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

			// states
		case "ReqGetStates":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: GetStates()", clientUuid)
				checksums, err := lrepository.GetStates()
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResGetStates",
					Payload: network.ResGetStates{
						Checksums: checksums,
						Err:       retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqPutState":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: PutState(%016x)", clientUuid, request.Payload.(network.ReqPutState).Checksum)
				data := request.Payload.(network.ReqPutState).Data
				datalen := int64(len(data))
				_, err := lrepository.PutState(request.Payload.(network.ReqPutState).Checksum, bytes.NewBuffer(data), datalen)
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResPutState",
					Payload: network.ResPutState{
						Err: retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqGetState":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: GetState(%016x)", clientUuid, request.Payload.(network.ReqGetState).Checksum)
				data, _, err := lrepository.GetState(request.Payload.(network.ReqGetState).Checksum)
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResGetState",
					Payload: network.ResGetState{
						Data: data,
						Err:  retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqDeleteState":
			wg.Add(1)
			go func() {
				defer wg.Done()

				logger.Trace("server", "%s: DeleteState(%s)", clientUuid, request.Payload.(network.ReqDeleteState).Checksum)

				var err error
				if options.NoDelete {
					err = fmt.Errorf("not allowed to delete")
				} else {
					err = lrepository.DeleteState(request.Payload.(network.ReqDeleteState).Checksum)
				}
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResDeleteState",
					Payload: network.ResDeleteState{
						Err: retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

			// packfiles
		case "ReqGetPackfiles":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: GetPackfiles()", clientUuid)
				checksums, err := lrepository.GetPackfiles()
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResGetPackfiles",
					Payload: network.ResGetPackfiles{
						Checksums: checksums,
						Err:       retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqPutPackfile":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: PutPackfile(%016x)", clientUuid, request.Payload.(network.ReqPutPackfile).Checksum)
				err := lrepository.PutPackfile(request.Payload.(network.ReqPutPackfile).Checksum,
					bytes.NewBuffer(request.Payload.(network.ReqPutPackfile).Data), uint64(len(request.Payload.(network.ReqPutPackfile).Data)))
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResPutPackfile",
					Payload: network.ResPutPackfile{
						Err: retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqGetPackfile":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: GetPackfile(%016x)", clientUuid, request.Payload.(network.ReqGetPackfile).Checksum)
				rd, _, err := lrepository.GetPackfile(request.Payload.(network.ReqGetPackfile).Checksum)
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}

				data, err := io.ReadAll(rd)
				if err != nil {
					retErr = err.Error()
				}

				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResGetPackfile",
					Payload: network.ResGetPackfile{
						Data: data,
						Err:  retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqGetPackfileBlob":
			wg.Add(1)
			go func() {
				defer wg.Done()
				logger.Trace("server", "%s: GetPackfileBlob(%016x, %d, %d)", clientUuid,
					request.Payload.(network.ReqGetPackfileBlob).Checksum,
					request.Payload.(network.ReqGetPackfileBlob).Offset,
					request.Payload.(network.ReqGetPackfileBlob).Length)
				rd, _, err := lrepository.GetPackfileBlob(request.Payload.(network.ReqGetPackfileBlob).Checksum,
					request.Payload.(network.ReqGetPackfileBlob).Offset,
					request.Payload.(network.ReqGetPackfileBlob).Length)
				retErr := ""
				var data []byte
				if err != nil {
					retErr = err.Error()
				} else {
					data, err = io.ReadAll(rd)
					if err != nil {
						retErr = err.Error()
					}
				}

				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResGetPackfileBlob",
					Payload: network.ResGetPackfileBlob{
						Data: data,
						Err:  retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		case "ReqDeletePackfile":
			wg.Add(1)
			go func() {
				defer wg.Done()

				logger.Trace("server", "%s: DeletePackfile(%s)", clientUuid, request.Payload.(network.ReqDeletePackfile).Checksum)

				var err error
				if options.NoDelete {
					err = fmt.Errorf("not allowed to delete")
				} else {
					err = lrepository.DeletePackfile(request.Payload.(network.ReqDeletePackfile).Checksum)
				}
				retErr := ""
				if err != nil {
					retErr = err.Error()
				}
				result := network.Request{
					Uuid: request.Uuid,
					Type: "ResDeletePackfile",
					Payload: network.ResDeletePackfile{
						Err: retErr,
					},
				}
				err = encoder.Encode(&result)
				if err != nil {
					logger.Warn("%s", err)
				}
			}()

		default:
			fmt.Println("Unknown request type", request.Type)
		}
	}
	wg.Wait()
}
