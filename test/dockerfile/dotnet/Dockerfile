# The managed runtime nobody in this suite covers. One process, threaded, glibc, and an exec form
# entrypoint: if this runs, what works here is not "java and go" but managed runtimes.
FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /src
RUN dotnet new web -o app --no-https && dotnet publish app -c Release -o /out

FROM mcr.microsoft.com/dotnet/aspnet:8.0
WORKDIR /app
COPY --from=build /out .
ENV ASPNETCORE_URLS=http://+:8080
EXPOSE 8080
ENTRYPOINT ["dotnet", "app.dll"]
