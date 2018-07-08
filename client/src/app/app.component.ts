import { Component, OnInit } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Observable, Subject, of } from 'rxjs';
import { NewsItem } from './news-item';
import { debounceTime, distinctUntilChanged, switchMap,  } from 'rxjs/operators';

@Component({
  selector: 'app-root',
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css']
})
export class AppComponent implements OnInit {
  private newsURL = '/news';
  private querySubject = new Subject<string>();
  newsListObservable: Observable<NewsItem[]>;


  constructor(private http: HttpClient) {}

  ngOnInit(): void {
    this.newsListObservable = this.querySubject.pipe(
      debounceTime(300),
      distinctUntilChanged(),
      switchMap((query: string) => this.getNewsList(query)),
    );
  }

  search(query: string): void {
    this.querySubject.next(query.trim());
  }

  getNewsList(query: string): Observable<NewsItem[]> {
    return (query.length > 0) ? this.http.get<NewsItem[]>(`${this.newsURL}?q=${query}`) : of(null);
  }
}
