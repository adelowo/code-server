package messaging

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	messagingpb "github.com/code-payments/code-protobuf-api/generated/go/messaging/v1"

	"github.com/code-payments/code-server/pkg/testutil"
)

func TestRendezvousProcess_HappyPath_OpenBeforeSend(t *testing.T) {
	for _, enableMultiServer := range []bool{true, false} {
		for _, enableKeepAlive := range []bool{true, false} {
			func() {
				env, cleanup := setup(t, enableMultiServer)
				defer cleanup()

				rendezvousKey := testutil.NewRandomAccount(t)

				env.client1.openMessageStream(t, rendezvousKey, enableKeepAlive)
				env.server1.assertInitialRendezvousRecordSaved(t, rendezvousKey)
				time.Sleep(500 * time.Millisecond) // allow async flush to finish

				sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
				sendMessageCall.requireSuccess(t)

				records := env.server1.getMessages(t, rendezvousKey)
				require.Len(t, records, 1)

				messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
				require.Len(t, messages, 1)

				env.client1.closeMessageStream(t, rendezvousKey)
				env.server1.assertRendezvousRecordDeleted(t, rendezvousKey)

				message := messages[0]
				assert.Equal(t, sendMessageCall.resp.MessageId.Value, message.Id.Value)

				env.client1.ackMessages(t, rendezvousKey, message.Id)
				env.server1.assertNoMessages(t, rendezvousKey)
			}()
		}
	}
}

func TestRendezvousProcess_HappyPath_OpenAfterSend(t *testing.T) {
	for _, enableMultiServer := range []bool{true, false} {
		for _, enableKeepAlive := range []bool{true, false} {
			func() {
				env, cleanup := setup(t, enableMultiServer)
				defer cleanup()

				rendezvousKey := testutil.NewRandomAccount(t)
				sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
				sendMessageCall.requireSuccess(t)

				records := env.server1.getMessages(t, rendezvousKey)
				require.Len(t, records, 1)

				env.client1.openMessageStream(t, rendezvousKey, enableKeepAlive)
				env.server1.assertInitialRendezvousRecordSaved(t, rendezvousKey)

				messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
				require.Len(t, messages, 1)

				env.client1.closeMessageStream(t, rendezvousKey)
				env.server1.assertRendezvousRecordDeleted(t, rendezvousKey)

				message := messages[0]
				assert.Equal(t, sendMessageCall.resp.MessageId.Value, message.Id.Value)

				env.client1.ackMessages(t, rendezvousKey, message.Id)
				env.server1.assertNoMessages(t, rendezvousKey)
			}()
		}
	}
}

func TestRendezvousProcess_MultipleOpenStreams(t *testing.T) {
	for i := 0; i < 32; i++ {
		for _, enableMultiServer := range []bool{true, false} {
			func() {
				env, cleanup := setup(t, enableMultiServer)
				defer cleanup()

				rendezvousKey := testutil.NewRandomAccount(t)

				for i := 0; i < 10; i++ {
					env.client1.openMessageStream(t, rendezvousKey, i%2 == 0)
				}
				time.Sleep(500 * time.Millisecond) // allow async flush to finish

				sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
				sendMessageCall.requireSuccess(t)

				records := env.server1.getMessages(t, rendezvousKey)
				require.Len(t, records, 1)

				messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
				require.Len(t, messages, 1)

				env.client1.closeMessageStream(t, rendezvousKey)

				message := messages[0]
				assert.Equal(t, sendMessageCall.resp.MessageId.Value, message.Id.Value)

				env.client1.ackMessages(t, rendezvousKey, message.Id)
				env.server1.assertNoMessages(t, rendezvousKey)
			}()
		}
	}
}

func TestRendezvousProcess_InternallyGeneratedMessage(t *testing.T) {
	for _, enableMultiServer := range []bool{true, false} {
		func() {
			env, cleanup := setup(t, enableMultiServer)
			defer cleanup()

			rendezvousKey := testutil.NewRandomAccount(t)

			env.client1.openMessageStream(t, rendezvousKey, false)
			time.Sleep(500 * time.Millisecond) // allow async flush to finish

			expectedMessage := &messagingpb.Message{
				Kind: &messagingpb.Message_CodeScanned{
					CodeScanned: &messagingpb.CodeScanned{
						Timestamp: timestamppb.Now(),
					},
				},
			}
			serverEnv := env.server1
			if enableMultiServer {
				serverEnv = env.server2
			}
			messageId, err := serverEnv.server.InternallyCreateMessage(serverEnv.ctx, rendezvousKey, expectedMessage)
			require.NoError(t, err)

			records := env.server1.getMessages(t, rendezvousKey)
			require.Len(t, records, 1)
			assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
			assert.Equal(t, messageId[:], records[0].MessageID[:])

			var savedProtoMessage messagingpb.Message
			require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

			assert.Equal(t, messageId[:], savedProtoMessage.Id.Value)
			require.NotNil(t, savedProtoMessage.GetCodeScanned())
			assert.True(t, proto.Equal(expectedMessage.GetCodeScanned(), savedProtoMessage.GetCodeScanned()))
			assert.Nil(t, savedProtoMessage.SendMessageRequestSignature)

			messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
			require.Len(t, messages, 1)

			env.client1.closeMessageStream(t, rendezvousKey)

			actualMessage := messages[0]
			assert.Equal(t, messageId[:], actualMessage.Id.Value)
			assert.True(t, proto.Equal(expectedMessage, actualMessage))

			env.client1.ackMessages(t, rendezvousKey, actualMessage.Id)
			env.server1.assertNoMessages(t, rendezvousKey)
		}()
	}
}

func TestSendMessage_RequestToGrabBill_HappyPath(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)
	sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
	sendMessageCall.requireSuccess(t)

	records := env.server1.getMessages(t, rendezvousKey)
	require.Len(t, records, 1)
	assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
	assert.Equal(t, sendMessageCall.resp.MessageId.Value, records[0].MessageID[:])

	var savedProtoMessage messagingpb.Message
	require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

	assert.Equal(t, sendMessageCall.resp.MessageId.Value, savedProtoMessage.Id.Value)
	require.NotNil(t, savedProtoMessage.GetRequestToGrabBill())
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToGrabBill().RequestorAccount.Value, savedProtoMessage.GetRequestToGrabBill().RequestorAccount.Value)
	assert.Equal(t, sendMessageCall.req.Signature.Value, savedProtoMessage.SendMessageRequestSignature.Value)

	env.client1.openMessageStream(t, rendezvousKey, false)
	messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
	env.client1.closeMessageStream(t, rendezvousKey)
	require.Len(t, messages, 1)
	assert.True(t, proto.Equal(&savedProtoMessage, messages[0]))
}

func TestSendMessage_RequestToGrabBill_Validation(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)

	env.client1.resetConf()
	env.client1.conf.simulateAccountNotCodeAccount = true
	sendMessageCall := env.client1.sendRequestToGrabBillMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "requestor account must be a temporary incoming account")
	env.server1.assertNoMessages(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateInvalidAccountType = true
	sendMessageCall = env.client1.sendRequestToGrabBillMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "requestor account must be a temporary incoming account")
	env.server1.assertNoMessages(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateStaleRequestorAccountType = true
	sendMessageCall = env.client1.sendRequestToGrabBillMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "requestor account must be latest temporary incoming account")
	env.server1.assertNoMessages(t, rendezvousKey)

}

func TestSendMessage_RequestToReceiveBill_KinValue_HappyPath(t *testing.T) {
	for _, isCodifyTrial := range []bool{true, false} {
		for _, disableDomainVerification := range []bool{true, false} {
			env, cleanup := setup(t, false)
			defer cleanup()

			rendezvousKey := testutil.NewRandomAccount(t)
			sendMessageCall := env.client2.sendRequestToReceiveKinBillMessage(t, rendezvousKey, isCodifyTrial, disableDomainVerification)
			sendMessageCall.requireSuccess(t)

			records := env.server1.getMessages(t, rendezvousKey)
			require.Len(t, records, 1)
			assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
			assert.Equal(t, sendMessageCall.resp.MessageId.Value, records[0].MessageID[:])

			var savedProtoMessage messagingpb.Message
			require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

			assert.Equal(t, sendMessageCall.resp.MessageId.Value, savedProtoMessage.Id.Value)
			require.NotNil(t, savedProtoMessage.GetRequestToReceiveBill())
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().RequestorAccount.Value, savedProtoMessage.GetRequestToReceiveBill().RequestorAccount.Value)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetExact().Currency, savedProtoMessage.GetRequestToReceiveBill().GetExact().Currency)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetExact().NativeAmount, savedProtoMessage.GetRequestToReceiveBill().GetExact().NativeAmount)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetExact().ExchangeRate, savedProtoMessage.GetRequestToReceiveBill().GetExact().ExchangeRate)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetExact().Quarks, savedProtoMessage.GetRequestToReceiveBill().GetExact().Quarks)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Domain.Value, savedProtoMessage.GetRequestToReceiveBill().Domain.Value)
			if disableDomainVerification {
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().Verifier)
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().Signature)
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().RendezvousKey)
			} else {
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Verifier.Value, savedProtoMessage.GetRequestToReceiveBill().Verifier.Value)
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Signature.Value, savedProtoMessage.GetRequestToReceiveBill().Signature.Value)
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().RendezvousKey.Value, savedProtoMessage.GetRequestToReceiveBill().RendezvousKey.Value)
			}
			assert.Equal(t, sendMessageCall.req.Signature.Value, savedProtoMessage.SendMessageRequestSignature.Value)

			env.server1.assertPaymentRequestRecordSaved(t, rendezvousKey, sendMessageCall.req.Message.GetRequestToReceiveBill())

			env.client1.openMessageStream(t, rendezvousKey, false)
			messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
			env.client1.closeMessageStream(t, rendezvousKey)
			require.Len(t, messages, 1)
			assert.True(t, proto.Equal(&savedProtoMessage, messages[0]))
		}
	}
}

func TestSendMessage_RequestToReceiveBill_FiatValue_HappyPath(t *testing.T) {
	for _, isCodifyTrial := range []bool{true, false} {
		for _, disableDomainVerification := range []bool{true, false} {
			env, cleanup := setup(t, false)
			defer cleanup()

			rendezvousKey := testutil.NewRandomAccount(t)
			sendMessageCall := env.client2.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, isCodifyTrial, disableDomainVerification)
			sendMessageCall.requireSuccess(t)

			records := env.server1.getMessages(t, rendezvousKey)
			require.Len(t, records, 1)
			assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
			assert.Equal(t, sendMessageCall.resp.MessageId.Value, records[0].MessageID[:])

			var savedProtoMessage messagingpb.Message
			require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

			assert.Equal(t, sendMessageCall.resp.MessageId.Value, savedProtoMessage.Id.Value)
			require.NotNil(t, savedProtoMessage.GetRequestToReceiveBill())
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().RequestorAccount.Value, savedProtoMessage.GetRequestToReceiveBill().RequestorAccount.Value)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetPartial().Currency, savedProtoMessage.GetRequestToReceiveBill().GetPartial().Currency)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().GetPartial().NativeAmount, savedProtoMessage.GetRequestToReceiveBill().GetPartial().NativeAmount)
			assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Domain.Value, savedProtoMessage.GetRequestToReceiveBill().Domain.Value)
			if disableDomainVerification {
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().Verifier)
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().Signature)
				assert.Nil(t, savedProtoMessage.GetRequestToReceiveBill().RendezvousKey)
			} else {
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Verifier.Value, savedProtoMessage.GetRequestToReceiveBill().Verifier.Value)
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().Signature.Value, savedProtoMessage.GetRequestToReceiveBill().Signature.Value)
				assert.Equal(t, sendMessageCall.req.Message.GetRequestToReceiveBill().RendezvousKey.Value, savedProtoMessage.GetRequestToReceiveBill().RendezvousKey.Value)
			}
			assert.Equal(t, sendMessageCall.req.Signature.Value, savedProtoMessage.SendMessageRequestSignature.Value)

			env.server1.assertPaymentRequestRecordSaved(t, rendezvousKey, sendMessageCall.req.Message.GetRequestToReceiveBill())

			env.client1.openMessageStream(t, rendezvousKey, false)
			messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
			env.client1.closeMessageStream(t, rendezvousKey)
			require.Len(t, messages, 1)
			assert.True(t, proto.Equal(&savedProtoMessage, messages[0]))
		}
	}
}

func TestSendMessage_RequestToReceiveBill_KinValue_Validation(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)

	//
	// Part 1: Account validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidAccountType = true
	sendMessageCall := env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "requestor account must be a primary account for trials using a code account")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 2: Exchange data validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidExchangeRate = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "kin exchange rate must be 1")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateInvalidNativeAmount = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "payment native amount and quark value mismatch")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateSmallNativeAmount = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "kin currency has a minimum amount of 5000.00")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateLargeNativeAmount = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "kin currency has a maximum amount of 100000.00")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateInvalidCurrency = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "exact exchange data only supports kin currency")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 3: Domain Validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidDomain = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertInvalidMessageError(t, "domain is invalid")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateDoesntOwnDomain = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertPermissionDeniedError(t, "does not own domain getcode.com")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 4: Signature validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidMessageSignature = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertUnauthenticatedError(t, "")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 5: Rendezvous key validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidRendezvousKey = true
	sendMessageCall = env.client1.sendRequestToReceiveKinBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertInvalidMessageError(t, "rendezvous key mismatch")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)
}

func TestSendMessage_RequestToReceiveBill_FiatValue_Validation(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)

	//
	// Part 1: Account validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidAccountType = true
	sendMessageCall := env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "requestor account must be a primary account for trials using a code account")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 2: Exchange data validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateSmallNativeAmount = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "usd currency has a minimum amount of 0.05")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateLargeNativeAmount = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "usd currency has a maximum amount of 1.00")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateInvalidCurrency = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, true)
	sendMessageCall.assertInvalidMessageError(t, "partial exchange data only supports fiat currencies")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 3: Domain Validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidDomain = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertInvalidMessageError(t, "domain is invalid")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateDoesntOwnDomain = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertPermissionDeniedError(t, "does not own domain getcode.com")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 4: Signature validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidMessageSignature = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertUnauthenticatedError(t, "")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)

	//
	// Part 5: Rendezvous key validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidRendezvousKey = true
	sendMessageCall = env.client1.sendRequestToReceiveFiatBillMessage(t, rendezvousKey, false, false)
	sendMessageCall.assertInvalidMessageError(t, "rendezvous key mismatch")
	env.server1.assertNoMessages(t, rendezvousKey)
	env.server1.assertPaymentRequestRecordNotSaved(t, rendezvousKey)
}

func TestSendMessage_RequestToLogin_HappyPath(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)
	sendMessageCall := env.client2.sendRequestToLoginMessage(t, rendezvousKey)
	sendMessageCall.requireSuccess(t)

	records := env.server1.getMessages(t, rendezvousKey)
	require.Len(t, records, 1)
	assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
	assert.Equal(t, sendMessageCall.resp.MessageId.Value, records[0].MessageID[:])

	var savedProtoMessage messagingpb.Message
	require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

	assert.Equal(t, sendMessageCall.resp.MessageId.Value, savedProtoMessage.Id.Value)
	require.NotNil(t, savedProtoMessage.GetRequestToLogin())
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToLogin().Verifier.Value, savedProtoMessage.GetRequestToLogin().Verifier.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToLogin().Domain.Value, savedProtoMessage.GetRequestToLogin().Domain.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToLogin().Nonce.Value, savedProtoMessage.GetRequestToLogin().Nonce.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToLogin().Signature.Value, savedProtoMessage.GetRequestToLogin().Signature.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetRequestToLogin().RendezvousKey.Value, savedProtoMessage.GetRequestToLogin().RendezvousKey.Value)
	assert.Equal(t, sendMessageCall.req.Signature.Value, savedProtoMessage.SendMessageRequestSignature.Value)

	env.client1.openMessageStream(t, rendezvousKey, false)
	messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
	env.client1.closeMessageStream(t, rendezvousKey)
	require.Len(t, messages, 1)
	assert.True(t, proto.Equal(&savedProtoMessage, messages[0]))
}

func TestSendMessage_RequestToLogin_Validation(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)

	//
	// Part 1: Domain validation

	env.client1.resetConf()
	env.client1.conf.simulateInvalidDomain = true
	sendMessageCall := env.client1.sendRequestToLoginMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "domain is invalid")
	env.server1.assertNoMessages(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateDoesntOwnDomain = true
	sendMessageCall = env.client1.sendRequestToLoginMessage(t, rendezvousKey)
	sendMessageCall.assertPermissionDeniedError(t, "does not own domain getcode.com")
	env.server1.assertNoMessages(t, rendezvousKey)

	//
	// Part 2: Signature validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidMessageSignature = true
	sendMessageCall = env.client1.sendRequestToLoginMessage(t, rendezvousKey)
	sendMessageCall.assertUnauthenticatedError(t, "")
	env.server1.assertNoMessages(t, rendezvousKey)

	//
	// Part 3: Rendezvous key validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidRendezvousKey = true
	sendMessageCall = env.client1.sendRequestToLoginMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "rendezvous key mismatch")
	env.server1.assertNoMessages(t, rendezvousKey)
}

func TestSendMessage_LoginAttempt_HappyPath(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)
	sendMessageCall := env.client2.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.requireSuccess(t)

	records := env.server1.getMessages(t, rendezvousKey)
	require.Len(t, records, 1)
	assert.Equal(t, rendezvousKey.PublicKey().ToBase58(), records[0].Account)
	assert.Equal(t, sendMessageCall.resp.MessageId.Value, records[0].MessageID[:])

	var savedProtoMessage messagingpb.Message
	require.NoError(t, proto.Unmarshal(records[0].Message, &savedProtoMessage))

	assert.Equal(t, sendMessageCall.resp.MessageId.Value, savedProtoMessage.Id.Value)
	require.NotNil(t, savedProtoMessage.GetLoginAttempt())
	assert.Equal(t, sendMessageCall.req.Message.GetLoginAttempt().UserId.Value, savedProtoMessage.GetLoginAttempt().UserId.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetLoginAttempt().Domain.Value, savedProtoMessage.GetLoginAttempt().Domain.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetLoginAttempt().Nonce.Value, savedProtoMessage.GetLoginAttempt().Nonce.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetLoginAttempt().Signature.Value, savedProtoMessage.GetLoginAttempt().Signature.Value)
	assert.Equal(t, sendMessageCall.req.Message.GetLoginAttempt().RendezvousKey.Value, savedProtoMessage.GetLoginAttempt().RendezvousKey.Value)
	assert.Equal(t, sendMessageCall.req.Signature.Value, savedProtoMessage.SendMessageRequestSignature.Value)

	env.client1.openMessageStream(t, rendezvousKey, false)
	messages := env.client1.receiveMessagesInRealTime(t, rendezvousKey)
	env.client1.closeMessageStream(t, rendezvousKey)
	require.Len(t, messages, 1)
	assert.True(t, proto.Equal(&savedProtoMessage, messages[0]))
}

func TestSendMessage_LoginAttempt_Validation(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)

	//
	// Part 1: Account validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidAccountType = true
	sendMessageCall := env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "account type must be RELATIONSHIP")
	env.server1.assertNoMessages(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateInvalidRelationship = true
	sendMessageCall = env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "account must have a relationship to getcode.com")
	env.server1.assertNoMessages(t, rendezvousKey)

	env.client1.resetConf()
	env.client1.conf.simulateAccountNotCodeAccount = true
	sendMessageCall = env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "account doesn't exist")
	env.server1.assertNoMessages(t, rendezvousKey)

	//
	// Part 2: Domain validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidDomain = true
	sendMessageCall = env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "domain is invalid")
	env.server1.assertNoMessages(t, rendezvousKey)

	//
	// Part 3: Signature validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidMessageSignature = true
	sendMessageCall = env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertUnauthenticatedError(t, "")
	env.server1.assertNoMessages(t, rendezvousKey)

	//
	// Part 4: Rendezvous key validation
	//

	env.client1.resetConf()
	env.client1.conf.simulateInvalidRendezvousKey = true
	sendMessageCall = env.client1.sendLoginAttemptMessage(t, rendezvousKey)
	sendMessageCall.assertInvalidMessageError(t, "rendezvous key mismatch")
	env.server1.assertNoMessages(t, rendezvousKey)
}

func TestMessagePolling_HappyPath(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	rendezvousKey := testutil.NewRandomAccount(t)
	sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
	sendMessageCall.requireSuccess(t)

	messages := env.client1.pollForMessages(t, rendezvousKey)
	require.Len(t, messages, 1)

	message := messages[0]
	assert.Equal(t, sendMessageCall.resp.MessageId.Value, message.Id.Value)
	assert.Equal(t, sendMessageCall.req.Signature.Value, message.SendMessageRequestSignature.Value)
	require.NotNil(t, message.GetRequestToGrabBill())
	assert.EqualValues(t, sendMessageCall.req.Message.GetRequestToGrabBill().RequestorAccount.Value, message.GetRequestToGrabBill().RequestorAccount.Value)

	env.client1.ackMessages(t, rendezvousKey, sendMessageCall.resp.MessageId)
	messages = env.client1.pollForMessages(t, rendezvousKey)
	require.Empty(t, messages)
}

// todo: need configurable timeouts so this can run faster
func TestKeepAlive_HappyPath(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	absoluteTimeout := rendezvousRecordMaxAge

	start := time.Now()
	rendezvousKey := testutil.NewRandomAccount(t)
	env.client1.openMessageStream(t, rendezvousKey, true)
	env.server1.assertInitialRendezvousRecordSaved(t, rendezvousKey)

	pingCount := env.client1.waitUntilStreamTerminationOrTimeout(t, rendezvousKey, true, absoluteTimeout)
	assert.True(t, time.Since(start) >= absoluteTimeout)
	assert.True(t, pingCount >= int(absoluteTimeout/messageStreamPingDelay))
	assert.True(t, pingCount <= int(absoluteTimeout/messageStreamPingDelay)+2)
	env.server1.assertRendezvousRecordRefreshed(t, rendezvousKey)
}

// todo: need configurable timeouts so this can run faster
func TestKeepAlive_UnresponsiveClient(t *testing.T) {
	env, cleanup := setup(t, false)
	defer cleanup()

	absoluteTimeout := rendezvousRecordMaxAge

	start := time.Now()
	rendezvousKey := testutil.NewRandomAccount(t)
	env.client1.openMessageStream(t, rendezvousKey, true)

	pingCount := env.client1.waitUntilStreamTerminationOrTimeout(t, rendezvousKey, false, absoluteTimeout)
	assert.True(t, time.Since(start) >= messageStreamKeepAliveRecvTimeout)
	assert.True(t, time.Since(start) <= messageStreamKeepAliveRecvTimeout+50*time.Millisecond)
	assert.True(t, pingCount >= int(messageStreamKeepAliveRecvTimeout/messageStreamPingDelay))
	assert.True(t, pingCount <= int(messageStreamKeepAliveRecvTimeout/messageStreamPingDelay)+1)
}

func TestRendezvousProcess_NoActiveStream(t *testing.T) {
	// Manually run when needed since time consuming
	//
	// todo: Improve testing
	t.Skip()

	for _, enableKeepAlive := range []bool{true, false} {
		env, cleanup := setup(t, false)
		defer cleanup()

		rendezvousKey := testutil.NewRandomAccount(t)

		env.client1.openMessageStream(t, rendezvousKey, enableKeepAlive)
		time.Sleep(time.Second)
		env.client1.closeMessageStream(t, rendezvousKey)

		time.Sleep(time.Minute)

		sendMessageCall := env.client2.sendRequestToGrabBillMessage(t, rendezvousKey)
		sendMessageCall.assertNoActiveStreamError(t)
		env.server1.assertNoMessages(t, rendezvousKey)
	}
}
