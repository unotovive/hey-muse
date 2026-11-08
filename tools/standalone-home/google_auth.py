"""Google カレンダー読み取り用の OAuth 同意を PC で行い、端末に送るトークンファイルを作る。"""
import argparse
import json
import os
import sys
from pathlib import Path

SCOPES = ['https://www.googleapis.com/auth/calendar.readonly']
DEFAULT_OUT = Path.home() / '.config/hey-muse/google-token.json'


def token_payload(creds):
    """端末に必要な最小限（アクセストークンは含めない）。"""
    if not creds.refresh_token:
        sys.exit('同意画面で「オフラインアクセス」が付与されませんでした。prompt=consent で再実行してください')
    return {'client_id': creds.client_id, 'client_secret': creds.client_secret, 'refresh_token': creds.refresh_token}


def main():
    # 依存ライブラリは実行時だけ必要（テストは未インストールでも動く）。
    from google_auth_oauthlib.flow import InstalledAppFlow
    from googleapiclient.discovery import build

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client', required=True, type=Path, help='OAuth クライアント（デスクトップ）の JSON')
    parser.add_argument('--out', type=Path, default=DEFAULT_OUT, help='出力先（既定: %(default)s）')
    args = parser.parse_args()

    flow = InstalledAppFlow.from_client_secrets_file(str(args.client), SCOPES)
    creds = flow.run_local_server(port=0, prompt='consent', access_type='offline')
    payload = token_payload(creds)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as f:
        json.dump(payload, f)
    os.chmod(args.out, 0o600)
    print(f'保存しました: {args.out}')

    service = build('calendar', 'v3', credentials=creds)
    print('利用できるカレンダー（id と名前を home-local.json の calendars に書きます）:')
    for item in service.calendarList().list().execute().get('items', []):
        print(f"  {item['id']}\t{item.get('summary', '')}")


if __name__ == '__main__':
    main()
