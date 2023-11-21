%define debug_package %{nil}

Name:           invenv
Version:        ${VERSION}
Release:        1
Summary:        `invenv` is a tool to automatically create and run your Python scripts in a virtual environment with installed dependencies

License:        MIT
URL:            https://github.com/jsnjack/invenv
Source0:        %{name}-%{version}.tar.gz

BuildRequires:  golang, git

%description
It tries to simplify running Python scripts (or even applications!) by taking
from you the burden of creating and maintaining virtual environments.

%prep
%setup -n invenv-%{version}

%build
go build -ldflags="-X %{name}/cmd.Version=%{version}" -o %{name}

%install
install -D -m 0755 %{name} %{buildroot}/%{_bindir}/%{name}

%files
%{_bindir}/%{name}

%changelog
* Mon Nov 20 2023 Yauhen Shulitski <jsnjack@gmail.com>
- Initial package
