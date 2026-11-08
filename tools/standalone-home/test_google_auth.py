import unittest, types, google_auth


class PayloadTest(unittest.TestCase):
    def test_payload_has_only_what_the_device_needs(self):
        creds = types.SimpleNamespace(client_id='cid', client_secret='cs', refresh_token='rt', token='access-should-not-ship')
        self.assertEqual(google_auth.token_payload(creds), {'client_id': 'cid', 'client_secret': 'cs', 'refresh_token': 'rt'})

    def test_missing_refresh_token_is_an_error(self):
        with self.assertRaises(SystemExit):
            google_auth.token_payload(types.SimpleNamespace(client_id='cid', client_secret='cs', refresh_token=None))


if __name__ == '__main__':
    unittest.main()
