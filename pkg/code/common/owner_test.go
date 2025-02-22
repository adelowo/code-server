package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonpb "github.com/code-payments/code-protobuf-api/generated/go/common/v1"

	timelock_token_v1 "github.com/code-payments/code-server/pkg/solana/timelock/v1"
	code_data "github.com/code-payments/code-server/pkg/code/data"
	"github.com/code-payments/code-server/pkg/code/data/account"
	"github.com/code-payments/code-server/pkg/code/data/phone"
)

func TestGetOwnerMetadata_User12Words(t *testing.T) {
	ctx := context.Background()
	data := code_data.NewTestDataProvider()

	subsidizerAccount = newRandomTestAccount(t)

	owner := newRandomTestAccount(t)

	_, err := GetOwnerMetadata(ctx, data, owner)
	assert.Equal(t, ErrOwnerNotFound, err)

	// Initially phone verified, but OpenAccounts intent not created. Until an
	// account type is mapped, we assume a user 12 words, since that's the expected
	// path.

	verificationRecord := &phone.Verification{
		PhoneNumber:    "+12223334444",
		OwnerAccount:   owner.PublicKey().ToBase58(),
		LastVerifiedAt: time.Now(),
		CreatedAt:      time.Now(),
	}
	require.NoError(t, data.SavePhoneVerification(ctx, verificationRecord))

	actual, err := GetOwnerMetadata(ctx, data, owner)
	require.NoError(t, err)
	assert.Equal(t, actual.Account.PublicKey().ToBase58(), owner.PublicKey().ToBase58())
	assert.Equal(t, OwnerTypeUser12Words, actual.Type)
	assert.Equal(t, OwnerManagementStateNotFound, actual.State)
	require.NotNil(t, actual.VerificationRecord)
	assert.Equal(t, verificationRecord.PhoneNumber, actual.VerificationRecord.PhoneNumber)

	// Later calls intent to OpenAccounts

	timelockAccounts, err := owner.GetTimelockAccounts(timelock_token_v1.DataVersion1)
	require.NoError(t, err)

	timelockRecord := timelockAccounts.ToDBRecord()
	require.NoError(t, data.SaveTimelock(ctx, timelockRecord))

	accountInfoRecord := &account.Record{
		OwnerAccount:     owner.PublicKey().ToBase58(),
		AuthorityAccount: timelockRecord.VaultOwner,
		TokenAccount:     timelockRecord.VaultAddress,
		AccountType:      commonpb.AccountType_PRIMARY,
	}
	require.NoError(t, data.CreateAccountInfo(ctx, accountInfoRecord))

	actual, err = GetOwnerMetadata(ctx, data, owner)
	require.NoError(t, err)
	assert.Equal(t, actual.Account.PublicKey().ToBase58(), owner.PublicKey().ToBase58())
	assert.Equal(t, OwnerTypeUser12Words, actual.Type)
	assert.Equal(t, OwnerManagementStateCodeAccount, actual.State)
	require.NotNil(t, actual.VerificationRecord)
	assert.Equal(t, verificationRecord.PhoneNumber, actual.VerificationRecord.PhoneNumber)
}

func TestGetOwnerMetadata_RemoteSendGiftCard(t *testing.T) {
	ctx := context.Background()
	data := code_data.NewTestDataProvider()

	subsidizerAccount = newRandomTestAccount(t)

	owner := newRandomTestAccount(t)

	_, err := GetOwnerMetadata(ctx, data, owner)
	assert.Equal(t, ErrOwnerNotFound, err)

	// It's possible a malicious user could phone verify a gift card owner, which
	// we should ignore
	verificationRecord := &phone.Verification{
		PhoneNumber:    "+12223334444",
		OwnerAccount:   owner.PublicKey().ToBase58(),
		LastVerifiedAt: time.Now(),
		CreatedAt:      time.Now(),
	}
	require.NoError(t, data.SavePhoneVerification(ctx, verificationRecord))

	timelockAccounts, err := owner.GetTimelockAccounts(timelock_token_v1.DataVersion1)
	require.NoError(t, err)

	timelockRecord := timelockAccounts.ToDBRecord()
	require.NoError(t, data.SaveTimelock(ctx, timelockRecord))

	accountInfoRecord := &account.Record{
		OwnerAccount:     owner.PublicKey().ToBase58(),
		AuthorityAccount: timelockRecord.VaultOwner,
		TokenAccount:     timelockRecord.VaultAddress,
		AccountType:      commonpb.AccountType_REMOTE_SEND_GIFT_CARD,
	}
	require.NoError(t, data.CreateAccountInfo(ctx, accountInfoRecord))

	actual, err := GetOwnerMetadata(ctx, data, owner)
	require.NoError(t, err)
	assert.Equal(t, actual.Account.PublicKey().ToBase58(), owner.PublicKey().ToBase58())
	assert.Equal(t, OwnerTypeRemoteSendGiftCard, actual.Type)
	assert.Equal(t, OwnerManagementStateCodeAccount, actual.State)
	assert.Nil(t, actual.VerificationRecord)
}

func TestGetLatestTokenAccountRecordsForOwner(t *testing.T) {
	ctx := context.Background()
	data := code_data.NewTestDataProvider()

	subsidizerAccount = newRandomTestAccount(t)

	owner := newRandomTestAccount(t)

	actual, err := GetLatestTokenAccountRecordsForOwner(ctx, data, owner)
	require.NoError(t, err)
	assert.Empty(t, actual)

	authority1 := newRandomTestAccount(t)
	authority2 := newRandomTestAccount(t)

	for _, authorityAndType := range []struct {
		account     *Account
		accountType commonpb.AccountType
	}{
		{authority1, commonpb.AccountType_BUCKET_1_KIN},
		{authority2, commonpb.AccountType_BUCKET_10_KIN},
	} {
		timelockAccounts, err := authorityAndType.account.GetTimelockAccounts(timelock_token_v1.DataVersion1)
		require.NoError(t, err)

		timelockRecord := timelockAccounts.ToDBRecord()
		require.NoError(t, data.SaveTimelock(ctx, timelockRecord))

		accountInfoRecord := &account.Record{
			OwnerAccount:     owner.PublicKey().ToBase58(),
			AuthorityAccount: timelockRecord.VaultOwner,
			TokenAccount:     timelockRecord.VaultAddress,
			AccountType:      authorityAndType.accountType,
		}
		require.NoError(t, data.CreateAccountInfo(ctx, accountInfoRecord))
	}

	actual, err = GetLatestTokenAccountRecordsForOwner(ctx, data, owner)
	require.NoError(t, err)
	require.Len(t, actual, 2)

	records, ok := actual[commonpb.AccountType_BUCKET_1_KIN]
	require.True(t, ok)
	assert.Equal(t, records.General.AuthorityAccount, authority1.PublicKey().ToBase58())
	assert.Equal(t, records.General.AccountType, commonpb.AccountType_BUCKET_1_KIN)
	assert.Equal(t, records.Timelock.VaultOwner, authority1.PublicKey().ToBase58())
	assert.Equal(t, records.General.TokenAccount, records.Timelock.VaultAddress)

	records, ok = actual[commonpb.AccountType_BUCKET_10_KIN]
	require.True(t, ok)
	assert.Equal(t, records.General.AuthorityAccount, authority2.PublicKey().ToBase58())
	assert.Equal(t, records.General.AccountType, commonpb.AccountType_BUCKET_10_KIN)
	assert.Equal(t, records.Timelock.VaultOwner, authority2.PublicKey().ToBase58())
	assert.Equal(t, records.General.TokenAccount, records.Timelock.VaultAddress)
}
