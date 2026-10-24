#![no_std]

use soroban_sdk::{contract, contracterror, contractimpl, contracttype, token, Address, Env};

/// PaymentEscrow contract handles secure payment escrow for Usenova services
///
/// This contract manages payments between service consumers and providers,
/// ensuring funds are held securely until service delivery is confirmed.
#[contract]
pub struct PaymentEscrow;

#[contracttype]
pub enum DataKey {
    /// Escrow details keyed by escrow id
    Escrow(u64),
}

#[contracttype]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Escrow {
    pub consumer: Address,
    pub provider: Address,
    pub token: Address,
    pub amount: i128,
    pub status: EscrowStatus,
}

#[contracttype]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum EscrowStatus {
    /// Funds deposited, awaiting service delivery
    Pending,
    /// Service delivered, funds released to provider
    Released,
    /// Refunded to consumer
    Refunded,
}

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
#[repr(u32)]
pub enum Error {
    /// Escrow with the given id does not exist
    NotFound = 1,
    /// Escrow amount must be positive
    InvalidAmount = 2,
    /// Escrow is not in the pending state required for this operation
    NotPending = 3,
    /// Escrow id is already in use
    AlreadyExists = 4,
}

#[contractimpl]
impl PaymentEscrow {
    /// Initialize an escrow payment.
    ///
    /// Transfers `amount` of `token` from `consumer` into the contract, locking
    /// the funds until [`release`] or [`refund`] is called. The consumer must
    /// authorize the call.
    pub fn create_escrow(
        env: Env,
        escrow_id: u64,
        consumer: Address,
        provider: Address,
        token: Address,
        amount: i128,
    ) -> Result<(), Error> {
        if amount <= 0 {
            return Err(Error::InvalidAmount);
        }

        let key = DataKey::Escrow(escrow_id);
        if env.storage().persistent().has(&key) {
            return Err(Error::AlreadyExists);
        }

        consumer.require_auth();

        let contract = env.current_contract_address();
        token::Client::new(&env, &token).transfer(&consumer, &contract, &amount);

        let escrow = Escrow {
            consumer,
            provider,
            token,
            amount,
            status: EscrowStatus::Pending,
        };
        env.storage().persistent().set(&key, &escrow);

        Ok(())
    }

    /// Release funds to provider after service delivery.
    ///
    /// The consumer authorizes the release. Funds move from the contract to the
    /// provider and the escrow is marked [`EscrowStatus::Released`].
    pub fn release(env: Env, escrow_id: u64) -> Result<(), Error> {
        let key = DataKey::Escrow(escrow_id);
        let mut escrow: Escrow = env
            .storage()
            .persistent()
            .get(&key)
            .ok_or(Error::NotFound)?;

        if escrow.status != EscrowStatus::Pending {
            return Err(Error::NotPending);
        }

        escrow.consumer.require_auth();

        let contract = env.current_contract_address();
        token::Client::new(&env, &escrow.token).transfer(
            &contract,
            &escrow.provider,
            &escrow.amount,
        );

        escrow.status = EscrowStatus::Released;
        env.storage().persistent().set(&key, &escrow);

        Ok(())
    }

    /// Refund funds to consumer.
    ///
    /// The consumer authorizes the refund. Funds move from the contract back to
    /// the consumer and the escrow is marked [`EscrowStatus::Refunded`].
    pub fn refund(env: Env, escrow_id: u64) -> Result<(), Error> {
        let key = DataKey::Escrow(escrow_id);
        let mut escrow: Escrow = env
            .storage()
            .persistent()
            .get(&key)
            .ok_or(Error::NotFound)?;

        if escrow.status != EscrowStatus::Pending {
            return Err(Error::NotPending);
        }

        escrow.consumer.require_auth();

        let contract = env.current_contract_address();
        token::Client::new(&env, &escrow.token).transfer(
            &contract,
            &escrow.consumer,
            &escrow.amount,
        );

        escrow.status = EscrowStatus::Refunded;
        env.storage().persistent().set(&key, &escrow);

        Ok(())
    }

    /// Get escrow status.
    pub fn get_status(env: Env, escrow_id: u64) -> Result<EscrowStatus, Error> {
        let escrow: Escrow = env
            .storage()
            .persistent()
            .get(&DataKey::Escrow(escrow_id))
            .ok_or(Error::NotFound)?;
        Ok(escrow.status)
    }

    /// Get full escrow details.
    pub fn get_escrow(env: Env, escrow_id: u64) -> Result<Escrow, Error> {
        env.storage()
            .persistent()
            .get(&DataKey::Escrow(escrow_id))
            .ok_or(Error::NotFound)
    }
}

#[cfg(test)]
mod test {
    use super::*;
    use soroban_sdk::{
        testutils::{Address as _, Ledger as _},
        token::{StellarAssetClient, TokenClient},
        Env,
    };

    struct Setup {
        env: Env,
        contract_id: Address,
        token: Address,
        consumer: Address,
        provider: Address,
    }

    fn setup() -> Setup {
        let env = Env::default();
        env.mock_all_auths();

        let contract_id = env.register_contract(None, PaymentEscrow);
        let admin = Address::generate(&env);
        let token = env.register_stellar_asset_contract(admin);
        let consumer = Address::generate(&env);
        let provider = Address::generate(&env);

        StellarAssetClient::new(&env, &token).mint(&consumer, &1_000);

        Setup {
            env,
            contract_id,
            token,
            consumer,
            provider,
        }
    }

    #[test]
    fn create_escrow_locks_funds() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);

        assert_eq!(token.balance(&s.consumer), 600);
        assert_eq!(token.balance(&s.contract_id), 400);
        assert_eq!(client.get_status(&1), EscrowStatus::Pending);

        let escrow = client.get_escrow(&1);
        assert_eq!(escrow.amount, 400);
        assert_eq!(escrow.consumer, s.consumer);
        assert_eq!(escrow.provider, s.provider);
    }

    #[test]
    fn release_pays_provider() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.release(&1);

        assert_eq!(token.balance(&s.provider), 400);
        assert_eq!(token.balance(&s.contract_id), 0);
        assert_eq!(token.balance(&s.consumer), 600);
        assert_eq!(client.get_status(&1), EscrowStatus::Released);
    }

    #[test]
    fn refund_returns_consumer() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.refund(&1);

        assert_eq!(token.balance(&s.consumer), 1_000);
        assert_eq!(token.balance(&s.provider), 0);
        assert_eq!(token.balance(&s.contract_id), 0);
        assert_eq!(client.get_status(&1), EscrowStatus::Refunded);
    }

    #[test]
    fn cannot_release_twice() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.release(&1);

        let res = client.try_release(&1);
        assert_eq!(res, Err(Ok(Error::NotPending)));
    }

    #[test]
    fn cannot_refund_after_release() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.release(&1);

        let res = client.try_refund(&1);
        assert_eq!(res, Err(Ok(Error::NotPending)));
    }

    #[test]
    fn rejects_zero_and_negative_amounts() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        assert_eq!(
            client.try_create_escrow(&1, &s.consumer, &s.provider, &s.token, &0),
            Err(Ok(Error::InvalidAmount))
        );
        assert_eq!(
            client.try_create_escrow(&2, &s.consumer, &s.provider, &s.token, &-5),
            Err(Ok(Error::InvalidAmount))
        );
    }

    #[test]
    fn rejects_duplicate_escrow_id() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        let res = client.try_create_escrow(&1, &s.consumer, &s.provider, &s.token, &100);
        assert_eq!(res, Err(Ok(Error::AlreadyExists)));
    }

    #[test]
    fn unknown_escrow_errors() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        assert_eq!(client.try_get_status(&99), Err(Ok(Error::NotFound)));
        assert_eq!(client.try_release(&99), Err(Ok(Error::NotFound)));
        assert_eq!(client.try_refund(&99), Err(Ok(Error::NotFound)));
    }

    #[test]
    fn multiple_escrows_are_independent() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        let provider2 = Address::generate(&s.env);
        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &300);
        client.create_escrow(&2, &s.consumer, &provider2, &s.token, &200);

        client.release(&1);
        client.refund(&2);

        assert_eq!(token.balance(&s.provider), 300);
        assert_eq!(token.balance(&provider2), 0);
        assert_eq!(token.balance(&s.consumer), 700);
        assert_eq!(client.get_status(&1), EscrowStatus::Released);
        assert_eq!(client.get_status(&2), EscrowStatus::Refunded);
    }

    #[test]
    fn escrow_persists_across_ledgers() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        s.env.ledger().with_mut(|info| info.sequence_number = 100);
        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        s.env.ledger().with_mut(|info| info.sequence_number = 250);
        client.release(&1);

        assert_eq!(client.get_status(&1), EscrowStatus::Released);
    }

    // -----------------------------------------------------------------------
    // New tests added for issue #31
    // -----------------------------------------------------------------------

    /// create_escrow accepts the maximum representable i128 value.
    /// The contract only requires amount > 0; there is no upper-bound check.
    #[test]
    fn create_escrow_max_amount() {
        let env = Env::default();
        env.mock_all_auths();

        let contract_id = env.register_contract(None, PaymentEscrow);
        let admin = Address::generate(&env);
        let token = env.register_stellar_asset_contract(admin);
        let consumer = Address::generate(&env);
        let provider = Address::generate(&env);

        // Mint exactly i128::MAX to the consumer so the transfer succeeds.
        StellarAssetClient::new(&env, &token).mint(&consumer, &i128::MAX);

        let client = PaymentEscrowClient::new(&env, &contract_id);
        let token_client = TokenClient::new(&env, &token);

        client.create_escrow(&1, &consumer, &provider, &token, &i128::MAX);

        assert_eq!(token_client.balance(&consumer), 0);
        assert_eq!(token_client.balance(&contract_id), i128::MAX);
        assert_eq!(client.get_status(&1), EscrowStatus::Pending);

        let escrow = client.get_escrow(&1);
        assert_eq!(escrow.amount, i128::MAX);
    }

    /// create_escrow with the same address for consumer and provider is
    /// permitted by the contract — there is no restriction preventing it.
    /// The funds are locked in the contract and released back to the same
    /// address on release.
    #[test]
    fn create_escrow_same_consumer_and_provider() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        // Use the consumer as both consumer and provider.
        client.create_escrow(&1, &s.consumer, &s.consumer, &s.token, &400);

        // Funds are locked in the contract.
        assert_eq!(token.balance(&s.consumer), 600);
        assert_eq!(token.balance(&s.contract_id), 400);
        assert_eq!(client.get_status(&1), EscrowStatus::Pending);

        // Release pays the "provider" (same address), so the consumer gets
        // the funds back.
        client.release(&1);
        assert_eq!(token.balance(&s.consumer), 1_000);
        assert_eq!(token.balance(&s.contract_id), 0);
        assert_eq!(client.get_status(&1), EscrowStatus::Released);
    }

    /// Mirrors cannot_refund_after_release: refunding first and then trying
    /// to release must fail with NotPending.
    #[test]
    fn cannot_release_after_refund() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.refund(&1);

        let res = client.try_release(&1);
        assert_eq!(res, Err(Ok(Error::NotPending)));
    }

    /// get_escrow on a released escrow returns the full record with
    /// status Released.
    #[test]
    fn get_escrow_after_release_shows_released_status() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.release(&1);

        let escrow = client.get_escrow(&1);
        assert_eq!(escrow.status, EscrowStatus::Released);
        assert_eq!(escrow.amount, 400);
        assert_eq!(escrow.consumer, s.consumer);
        assert_eq!(escrow.provider, s.provider);
    }

    /// get_escrow on a refunded escrow returns the full record with
    /// status Refunded.
    #[test]
    fn get_escrow_after_refund_shows_refunded_status() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);

        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        client.refund(&1);

        let escrow = client.get_escrow(&1);
        assert_eq!(escrow.status, EscrowStatus::Refunded);
        assert_eq!(escrow.amount, 400);
        assert_eq!(escrow.consumer, s.consumer);
        assert_eq!(escrow.provider, s.provider);
    }

    /// Two escrows backed by different token contracts are fully independent:
    /// releasing one does not affect the balances or status of the other.
    #[test]
    fn two_escrows_different_tokens_are_independent() {
        let env = Env::default();
        env.mock_all_auths();

        let contract_id = env.register_contract(None, PaymentEscrow);
        let consumer = Address::generate(&env);
        let provider = Address::generate(&env);

        // Register two separate token contracts.
        let admin_a = Address::generate(&env);
        let admin_b = Address::generate(&env);
        let token_a = env.register_stellar_asset_contract(admin_a);
        let token_b = env.register_stellar_asset_contract(admin_b);

        StellarAssetClient::new(&env, &token_a).mint(&consumer, &500);
        StellarAssetClient::new(&env, &token_b).mint(&consumer, &500);

        let client = PaymentEscrowClient::new(&env, &contract_id);
        let ta = TokenClient::new(&env, &token_a);
        let tb = TokenClient::new(&env, &token_b);

        client.create_escrow(&1, &consumer, &provider, &token_a, &300);
        client.create_escrow(&2, &consumer, &provider, &token_b, &200);

        // Release escrow 1 (token A), refund escrow 2 (token B).
        client.release(&1);
        client.refund(&2);

        // Token A: provider received 300, consumer kept 200.
        assert_eq!(ta.balance(&provider), 300);
        assert_eq!(ta.balance(&consumer), 200);
        assert_eq!(ta.balance(&contract_id), 0);

        // Token B: consumer got 200 back, provider received nothing.
        assert_eq!(tb.balance(&consumer), 500);
        assert_eq!(tb.balance(&provider), 0);
        assert_eq!(tb.balance(&contract_id), 0);

        assert_eq!(client.get_status(&1), EscrowStatus::Released);
        assert_eq!(client.get_status(&2), EscrowStatus::Refunded);
    }

    /// Escrow created at ledger sequence 1 is still accessible and can be
    /// released after jumping to sequence 100_000, confirming persistent
    /// storage survives a large ledger gap.
    #[test]
    fn escrow_persists_across_large_ledger_gap() {
        let s = setup();
        let client = PaymentEscrowClient::new(&s.env, &s.contract_id);
        let token = TokenClient::new(&s.env, &s.token);

        s.env.ledger().with_mut(|info| info.sequence_number = 1);
        client.create_escrow(&1, &s.consumer, &s.provider, &s.token, &400);
        assert_eq!(client.get_status(&1), EscrowStatus::Pending);

        s.env
            .ledger()
            .with_mut(|info| info.sequence_number = 100_000);
        client.release(&1);

        assert_eq!(client.get_status(&1), EscrowStatus::Released);
        assert_eq!(token.balance(&s.provider), 400);
        assert_eq!(token.balance(&s.consumer), 600);
    }
}
