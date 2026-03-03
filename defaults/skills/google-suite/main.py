import sys
import argparse
import os
import io
import google_auth
from googleapiclient.http import MediaFileUpload, MediaIoBaseDownload

def drive_list_files(folder_name=None):
    service = google_auth.get_drive_service()
    query = "trashed = false"
    if folder_name:
        folder_query = f"name = '{folder_name}' and mimeType = 'application/vnd.google-apps.folder' and trashed = false"
        results = service.files().list(q=folder_query, fields="files(id, name)").execute()
        folders = results.get('files', [])
        if not folders:
            print(f"No folder found with name: {folder_name}")
            return
        folder_id = folders[0]['id']
        query = f"'{folder_id}' in parents and trashed = false"
    
    results = service.files().list(q=query, fields="files(id, name, mimeType)").execute()
    items = results.get('files', [])
    if not items:
        print('No files found.')
    else:
        for item in items:
            print(f"{item['name']} ({item['id']}) [{item['mimeType']}]")

def drive_download(file_id, local_path):
    service = google_auth.get_drive_service()
    request = service.files().get_media(fileId=file_id)
    fh = io.FileIO(local_path, 'wb')
    downloader = MediaIoBaseDownload(fh, request)
    done = False
    while done is False:
        status, done = downloader.next_chunk()
    print(f"File downloaded to {local_path}")

def drive_upload(local_path, folder_id=None, name=None):
    service = google_auth.get_drive_service()
    file_name = name if name else os.path.basename(local_path)
    file_metadata = {'name': file_name}
    if folder_id:
        file_metadata['parents'] = [folder_id]
    
    media = MediaFileUpload(local_path, resumable=True)
    file = service.files().create(body=file_metadata, media_body=media, fields='id').execute()
    print(f"File uploaded. ID: {file.get('id')}")

def drive_create_folder(name, parent_id=None):
    service = google_auth.get_drive_service()
    file_metadata = {
        'name': name,
        'mimeType': 'application/vnd.google-apps.folder'
    }
    if parent_id:
        file_metadata['parents'] = [parent_id]
    file = service.files().create(body=file_metadata, fields='id').execute()
    print(f"Folder created. ID: {file.get('id')}")

def main():
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest='command')

    subparsers.add_parser('list').add_argument('--folder', help='Folder name')
    
    p_dl = subparsers.add_parser('download')
    p_dl.add_argument('id')
    p_dl.add_argument('path')

    p_up = subparsers.add_parser('upload')
    p_up.add_argument('path')
    p_up.add_argument('--folder_id')
    p_up.add_argument('--name')

    p_mk = subparsers.add_parser('mkdir')
    p_mk.add_argument('name')
    p_mk.add_argument('--parent_id')

    args = parser.parse_args()

    if args.command == 'list': drive_list_files(args.folder)
    elif args.command == 'download': drive_download(args.id, args.path)
    elif args.command == 'upload': drive_upload(args.path, args.folder_id, args.name)
    elif args.command == 'mkdir': drive_create_folder(args.name, args.parent_id)

if __name__ == '__main__':
    main()
