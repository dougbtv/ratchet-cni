FROM centos:centos7
MAINTAINER Doug Smith <info@laboratoryb.org>
ENV build_date 2016-05-15

RUN yum install -y net-tools nano iproute
ADD entrypoint.sh /entrypoint.sh

ENTRYPOINT /entrypoint.sh

